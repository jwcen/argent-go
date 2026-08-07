package transport

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/cloudwego/eino/schema"

	"github.com/jwcen/argent-go/internal/agent"
)

// AgentHandler 把 LLM agent 适配成 HTTP 接口。
type AgentHandler struct {
	svc *agent.Service
}

func NewAgentHandler(svc *agent.Service) *AgentHandler {
	return &AgentHandler{svc: svc}
}

func (h *AgentHandler) Register(r gin.IRouter) {
	g := r.Group("/ask")
	g.POST("/stock", h.Ask)
	g.POST("/stock/stream", h.AskStream) // SSE 流式
}

type askReq struct {
	Message string `json:"message" binding:"required"`
}

// POST /api/ask/stock — 非流式问答
func (h *AgentHandler) Ask(c *gin.Context) {
	var req askReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "message is required")
		return
	}

	systemPrompt := "你是一个 A 股投资助手。用户会问你股票、持仓、行情相关的问题，请简洁回答。"
	messages := agent.BuildMessages(systemPrompt, req.Message)

	answer, err := h.svc.Chat(c.Request.Context(), messages)
	if err != nil {
		// LLM 未配置时返回友好错误
		if strings.Contains(err.Error(), "API key not configured") {
			WriteError(c, http.StatusServiceUnavailable, "LLM not configured")
			return
		}
		WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"answer": answer})
}

// POST /api/ask/stock/stream — SSE 流式问答
//
// 前端用 resp.body.getReader() 手工解析（非 EventSource）。
// Go 用 http.Flusher 逐块 flush。
func (h *AgentHandler) AskStream(c *gin.Context) {
	var req askReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "message is required")
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		WriteError(c, http.StatusInternalServerError, "streaming not supported")
		return
	}

	systemPrompt := "你是一个 A 股投资助手。"
	messages := agent.BuildMessages(systemPrompt, req.Message)

	stream, err := h.svc.ChatStream(c.Request.Context(), messages)
	if err != nil {
		// 流已经开始（200 已写），只能写错误事件
		c.Writer.Write([]byte("data: [ERROR] " + err.Error() + "\n\n"))
		flusher.Flush()
		return
	}

	for chunk := range stream {
		// SSE 格式：data: <chunk>\n\n
		_, _ = c.Writer.Write([]byte("data: " + chunk + "\n\n"))
		flusher.Flush()
	}

	// 结束标记
	c.Writer.Write([]byte("data: [DONE]\n\n"))
	flusher.Flush()
}

// 确保 schema 和 context 包被引用
var _ = schema.System
var _ = context.Background
var _ = errors.New
