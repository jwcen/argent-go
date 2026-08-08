package agent

import (
	"context"
	"errors"
)

// ErrSessionNotFound 表示会话不存在或不属于当前用户。
var ErrSessionNotFound = errors.New("ask session not found")

// MessageMeta 是单条消息的附加信息，前端用它还原卡片/图表/来源。
// 存库时序列化为 JSON 放到 ask_messages.meta 列。
type MessageMeta struct {
	Images    []string `json:"images,omitempty"`
	ToolsUsed []string `json:"tools_used,omitempty"`
	Sources   []Source `json:"sources,omitempty"`
	Charts    []string `json:"charts,omitempty"`
}

// Source 是回答引用的来源链接。
type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Session 是一次"问问市场"会话的元信息。
type Session struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
	MsgCount  int    `json:"msg_count"`
}

// Message 是会话里的一条消息（user 或 assistant）。
type Message struct {
	ID        int64       `json:"id"`
	SessionID int64       `json:"session_id"`
	Role      string      `json:"role"`
	Content   string      `json:"content"`
	Meta      MessageMeta `json:"meta"`
	CreatedAt string      `json:"created_at"`
}

// SessionStore 是会话持久化接口，定义在域包；sqlite 在 infra 层实现。
// 这样 agent 域不依赖具体存储框架，符合整洁架构依赖倒置原则。
type SessionStore interface {
	CreateSession(ctx context.Context, userID int64, title string) (*Session, error)
	ListSessions(ctx context.Context, userID int64) ([]Session, error)
	GetSession(ctx context.Context, userID, id int64) (*Session, error)
	DeleteSession(ctx context.Context, userID, id int64) error
	AppendMessage(ctx context.Context, userID, sessionID int64, role, content string, meta MessageMeta) (*Message, error)
	ListMessages(ctx context.Context, sessionID int64) ([]Message, error)
}
