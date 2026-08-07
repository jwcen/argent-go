// Package ws 实现 WebSocket 推送 hub。
//
// hub 模式：每用户一组连接，盘中 5s 推 price_update 报文。
// 报文契约（wiki/05）：{"type":"price_update","data":{...},"market_open":bool}
package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/jwcen/argent-go/internal/auth"
	"github.com/jwcen/argent-go/internal/market"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Hub 管理所有活跃的 WS 连接，按 userID 分组。
type Hub struct {
	mu      sync.RWMutex
	clients map[int64]map[*Client]struct{} // userID → 一组连接
	quoter  market.Quoter
	logger  *slog.Logger
	cancel  context.CancelFunc
}

// Client 单个 WS 连接。
type Client struct {
	conn   *websocket.Conn
	userID int64
	send   chan []byte
	hub    *Hub
}

func NewHub(quoter market.Quoter, logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{
		clients: make(map[int64]map[*Client]struct{}),
		quoter:  quoter,
		logger:  logger,
	}
}

// Start 启动价格推送 goroutine，盘中每 5s 推一次。
func (h *Hub) Start(ctx context.Context) {
	ctx, h.cancel = context.WithCancel(ctx)
	go h.priceLoop(ctx)
}

// Stop 停止推送。
func (h *Hub) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
}

// HandleWS 处理 /ws 升级请求。必须放在 RequireAuth 之后。
func (h *Hub) HandleWS(c *gin.Context) {
	user, ok := auth.UserFromContext(c.Request.Context())
	if !ok {
		c.Data(http.StatusUnauthorized, "application/json", []byte(`{"detail":"not authenticated"}`))
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Warn("ws upgrade failed", "err", err)
		return
	}

	client := &Client{
		conn:   conn,
		userID: user.ID,
		send:   make(chan []byte, 64),
		hub:    h,
	}

	h.register(client)
	go client.writePump()
	go client.readPump()
}

func (h *Hub) register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[c.userID] == nil {
		h.clients[c.userID] = make(map[*Client]struct{})
	}
	h.clients[c.userID][c] = struct{}{}
}

func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if clients, ok := h.clients[c.userID]; ok {
		delete(clients, c)
		if len(clients) == 0 {
			delete(h.clients, c.userID)
		}
	}
}

// priceLoop 盘中每 5s 广播价格。
func (h *Hub) priceLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !market.IsTradingDay(time.Now()) {
				continue
			}
			now := time.Now()
			// 9:30-11:30, 13:00-15:00
			hour, min := now.Hour(), now.Minute()
			marketOpen := (hour == 9 && min >= 30) || (hour >= 10 && hour < 11) ||
				(hour == 11 && min <= 30) || (hour >= 13 && hour < 15)
			if !marketOpen {
				continue
			}
			h.broadcastPrices(ctx, marketOpen)
		}
	}
}

func (h *Hub) broadcastPrices(ctx context.Context, marketOpen bool) {
	h.mu.RLock()
	userIDs := make([]int64, 0, len(h.clients))
	for uid := range h.clients {
		userIDs = append(userIDs, uid)
	}
	h.mu.RUnlock()

	if len(userIDs) == 0 {
		return
	}

	// 简化版：对所有用户推送相同的指数数据。
	// 完整版应按用户持仓调 quoter.Quote。
	idx, err := h.quoter.Quote(ctx, []string{"000001"})
	if err != nil {
		return
	}

	msg := map[string]any{
		"type":        "price_update",
		"data":        idx,
		"market_open": marketOpen,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	h.mu.RLock()
	for _, clients := range h.clients {
		for c := range clients {
			select {
			case c.send <- data:
			default: // 缓冲区满，跳过这条
			}
		}
	}
	h.mu.RUnlock()
}

// writePump 把 send channel 里的消息写到 WS。
func (c *Client) writePump() {
	defer c.conn.Close()
	for msg := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

// readPump 读客户端消息（主要是 ping/pong），丢弃即可。
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister(c)
		close(c.send)
		c.conn.Close()
	}()
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
	}
}
