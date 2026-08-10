package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/agent"
	"github.com/jwcen/argent-go/internal/auth"
	"github.com/jwcen/argent-go/internal/external"
	"github.com/jwcen/argent-go/internal/infra/sqlite"
	"github.com/jwcen/argent-go/internal/portfolio"
)

// AgentHandler 把 LLM agent 与问问市场会话持久化适配成 HTTP 接口。
//
// 会话数据存每用户独立库（users/u{id}.db），故通过 dbFn 按请求取库，
// 与 PortfolioHandler 的 per-request DB 模式一致。
type AgentHandler struct {
	svc  *agent.Service
	dbFn func(userID int64) (*sql.DB, error)
}

func NewAgentHandler(svc *agent.Service, dbFn func(userID int64) (*sql.DB, error)) *AgentHandler {
	return &AgentHandler{svc: svc, dbFn: dbFn}
}

func (h *AgentHandler) Register(r gin.IRouter) {
	g := r.Group("/ask")
	g.POST("/stock", h.Ask)
	g.POST("/stock/stream", h.AskStream) // SSE 流式
	// 会话持久化（前端 StockAsk 依赖）
	g.GET("/sessions", h.ListSessions)
	g.GET("/sessions/:id", h.GetSession)
	g.DELETE("/sessions/:id", h.DeleteSession)
	g.POST("/messages", h.AppendMessage)
}

// store 按当前请求用户构造一次性的会话存储。
func (h *AgentHandler) store(c *gin.Context) (agent.SessionStore, error) {
	uid := auth.MustUserID(c.Request.Context())
	if uid == 0 {
		return nil, errors.New("no user in context")
	}
	db, err := h.dbFn(uid)
	if err != nil {
		return nil, err
	}
	return sqlite.NewAskRepo(db), nil
}

// ---- 会话持久化 ----

type msgReq struct {
	SessionID int64             `json:"session_id"`
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	Title     string            `json:"title"`
	Meta      agent.MessageMeta `json:"meta"`
}

// GET /api/ask/sessions — 列出当前用户的历史会话。
func (h *AgentHandler) ListSessions(c *gin.Context) {
	store, err := h.store(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	sessions, err := store.ListSessions(c.Request.Context(), auth.MustUserID(c.Request.Context()))
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	if sessions == nil {
		sessions = []agent.Session{}
	}
	WriteJSON(c, http.StatusOK, gin.H{"sessions": sessions})
}

// GET /api/ask/sessions/:id — 取单个会话及其全部消息，用于还原对话。
func (h *AgentHandler) GetSession(c *gin.Context) {
	uid := auth.MustUserID(c.Request.Context())
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		WriteError(c, http.StatusBadRequest, "invalid session id")
		return
	}
	store, err := h.store(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	sess, err := store.GetSession(c.Request.Context(), uid, id)
	if errors.Is(err, agent.ErrSessionNotFound) {
		WriteError(c, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	messages, err := store.ListMessages(c.Request.Context(), id)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{
		"id":         sess.ID,
		"title":      sess.Title,
		"updated_at": sess.UpdatedAt,
		"messages":   messages,
	})
}

// DELETE /api/ask/sessions/:id — 删除一个会话及其消息。
func (h *AgentHandler) DeleteSession(c *gin.Context) {
	uid := auth.MustUserID(c.Request.Context())
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		WriteError(c, http.StatusBadRequest, "invalid session id")
		return
	}
	store, err := h.store(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := store.DeleteSession(c.Request.Context(), uid, id); errors.Is(err, agent.ErrSessionNotFound) {
		WriteError(c, http.StatusNotFound, "session not found")
		return
	} else if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

// POST /api/ask/messages — 持久化一条消息。
// 语义对齐前端 persistTurn：session_id 为 0 时新建会话（用 title 作标题），
// 返回分配的 session_id；否则追加到该会话。
func (h *AgentHandler) AppendMessage(c *gin.Context) {
	var req msgReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Role != "user" && req.Role != "assistant" {
		WriteError(c, http.StatusBadRequest, "role must be user or assistant")
		return
	}
	uid := auth.MustUserID(c.Request.Context())
	store, err := h.store(c)
	if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	sid := req.SessionID
	if sid == 0 {
		title := req.Title
		if title == "" {
			title = firstLine(req.Content)
		}
		sess, err := store.CreateSession(c.Request.Context(), uid, title)
		if err != nil {
			WriteError(c, http.StatusInternalServerError, err.Error())
			return
		}
		sid = sess.ID
	}

	msg, err := store.AppendMessage(c.Request.Context(), uid, sid, req.Role, req.Content, req.Meta)
	if errors.Is(err, agent.ErrSessionNotFound) {
		WriteError(c, http.StatusNotFound, "session not found")
		return
	} else if err != nil {
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"session_id": sid, "id": msg.ID})
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 40 {
		return s[:40]
	}
	return s
}

// ---- 问答 ----

type askStreamReq struct {
	Question string               `json:"question" binding:"required"`
	History  []agent.HistoryTurn  `json:"history"`
	Images   []string             `json:"images"`
}

// POST /api/ask/stock — 非流式问答（前端主路径使用 /stock/stream）。
func (h *AgentHandler) Ask(c *gin.Context) {
	var req askStreamReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "question is required")
		return
	}

	systemPrompt := "你是一个 A 股投资助手。用户会问你股票、持仓、行情相关的问题，请简洁回答。"
	messages := agent.BuildMessagesWithHistory(systemPrompt, req.History, req.Question)

	answer, err := h.svc.Chat(c.Request.Context(), messages)
	if err != nil {
		if strings.Contains(err.Error(), "API key not configured") {
			WriteError(c, http.StatusServiceUnavailable, "LLM not configured")
			return
		}
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"answer": answer})
}

// POST /api/ask/stock/stream — SSE 流式问答。
//
// 前端用 resp.body.getReader() 手工解析：每行形如 `data: <json>\n\n`，
// json 含 {type:'answer', text} 或 {type:'error', error}。
func (h *AgentHandler) AskStream(c *gin.Context) {
	var req askStreamReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "question is required")
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		writeSSE(c, flusher, map[string]any{"type": "error", "error": "streaming not supported"})
		return
	}

	// 未配置 LLM：走本地演示降级，让流式交互与持久化链路可见。
	if !h.svc.IsConfigured() {
		mock := "[演示模式] 未检测到 ARGENT_LLM_API_KEY，以下为本地模拟回复。\n\n你问的是：「" +
			req.Question + "」\n\n配置 ARGENT_LLM_API_KEY（OpenAI 兼容）后，这里会接入真实 LLM 进行回答。"
		for _, chunk := range mockChunks(mock, 14) {
			writeSSE(c, flusher, map[string]any{"type": "answer", "text": chunk})
		}
		return
	}

	systemPrompt := "你是一个 A 股投资助手，基于行情、走势、新闻与大盘情绪客观解读。"
	messages := agent.BuildMessagesWithHistory(systemPrompt, req.History, req.Question)

	// 当前用户身份 → 构造用户级 service，注入 ctx 供用户数据类工具(get_holdings 等)使用。
	uid := auth.MustUserID(c.Request.Context())
	db, dbErr := h.dbFn(uid)
	if dbErr != nil {
		writeSSE(c, flusher, map[string]any{"type": "error", "error": "取用户库失败: " + dbErr.Error()})
		return
	}
	pSvc := portfolio.NewService(sqlite.NewPortfolioRepo(db))
	eSvc := external.NewService(sqlite.NewExternalRepo(db))
	ctx := agent.WithUserServices(c.Request.Context(), &portfolioStoreAdapter{pSvc}, &externalStoreAdapter{eSvc})

	// 工具事件 → SSE 实时透给前端。工具回调与文本循环分属不同 goroutine，
	// 用同一把锁串行化 SSE 写入，避免 ResponseWriter 并发写损坏。
	var mu sync.Mutex
	safeWrite := func(ev map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		writeSSE(c, flusher, ev)
	}
	ctx = agent.WithToolSink(ctx, func(ev agent.ToolEvent) {
		// 长结果/参数截断展示，避免前端卡片爆栈；完整结果已在模型上下文里。
		if len(ev.Result) > 800 {
			ev.Result = ev.Result[:800] + "…"
		}
		if len(ev.Args) > 400 {
			ev.Args = ev.Args[:400] + "…"
		}
		safeWrite(map[string]any{"type": "tool", "tool": ev})
	})

	stream, err := h.svc.ChatStreamReAct(ctx, messages)
	if err != nil {
		writeSSE(c, flusher, map[string]any{"type": "error", "error": err.Error()})
		return
	}

	for chunk := range stream {
		if chunk == "" {
			continue
		}
		safeWrite(map[string]any{"type": "answer", "text": chunk})
	}
}

// writeSSE 写出一个 SSE 事件：data: <json>\n\n 并 flush。
func writeSSE(c *gin.Context, f http.Flusher, ev any) {
	b, _ := json.Marshal(ev)
	c.Writer.Write([]byte("data: " + string(b) + "\n\n"))
	if f != nil {
		f.Flush()
	}
}

// ── agent 用户级 service 适配器 ──
// 把真实 *portfolio.Service / *external.Service 适配成 agent 所需的
// 最小接口，并在转换时把真实类型映射为 agent 领域结构（避免 agent 包反向依赖 sqlite）。

type portfolioStoreAdapter struct{ svc *portfolio.Service }

func (a *portfolioStoreAdapter) ListHoldings(ctx context.Context) ([]agent.PortfolioHolding, error) {
	hs, err := a.svc.ListHoldings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]agent.PortfolioHolding, 0, len(hs))
	for _, h := range hs {
		out = append(out, agent.PortfolioHolding{
			StockCode: h.StockCode, StockName: h.StockName, Shares: h.Shares,
			CostPrice: h.CostPrice, WeightedDays: h.WeightedDays,
		})
	}
	return out, nil
}

func (a *portfolioStoreAdapter) ListActions(ctx context.Context, code string) ([]agent.PortfolioAction, error) {
	var (
		acts []portfolio.Action
		err  error
	)
	if code == "" {
		acts, err = a.svc.ListAllActions(ctx)
	} else {
		acts, err = a.svc.ListActions(ctx, code)
	}
	if err != nil {
		return nil, err
	}
	out := make([]agent.PortfolioAction, 0, len(acts))
	for _, ac := range acts {
		out = append(out, agent.PortfolioAction{
			StockCode: ac.StockCode, ActionType: string(ac.ActionType),
			Price: ac.Price, Shares: ac.Shares, TradeDate: ac.TradeDate, Note: ac.Note,
		})
	}
	return out, nil
}

func (a *portfolioStoreAdapter) GetThesis(ctx context.Context, code string) (*agent.PortfolioThesis, error) {
	t, err := a.svc.GetThesis(ctx, code)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, nil
	}
	return &agent.PortfolioThesis{Code: t.Code, Name: t.Name, Thesis: t.Thesis}, nil
}

type externalStoreAdapter struct{ svc *external.Service }

func (a *externalStoreAdapter) ListAssets(ctx context.Context) ([]agent.ExternalAsset, error) {
	as, err := a.svc.ListAssets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]agent.ExternalAsset, 0, len(as))
	for _, a2 := range as {
		out = append(out, agent.ExternalAsset{
			AssetType: string(a2.AssetType), Name: a2.Name,
			CostAmount: a2.CostAmount, Shares: a2.Shares,
		})
	}
	return out, nil
}

// mockChunks 把文本切成定长片段，模拟打字机式流式输出。
// 必须按 rune 切片，否则多字节 UTF-8 中文会被从中间截断产生乱码。
func mockChunks(s string, size int) []string {
	runes := []rune(s)
	var out []string
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

var _ = context.Background
