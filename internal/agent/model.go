package agent

import (
	"context"
	"fmt"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// newOpenAIModel 用 eino-ext 的 OpenAI 适配器创建 ChatModel。
// Anthropic 也提供 OpenAI 兼容端点，所以统一走这条路径。
func newOpenAIModel(ctx context.Context, cfg Config) (model.ChatModel, error) {
	m, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:   cfg.Model,
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("agent: openai model: %w", err)
	}
	return m, nil
}

// BuildMessages 把用户输入组装成 schema.Message 列表。
func BuildMessages(systemPrompt, userInput string) []*schema.Message {
	return []*schema.Message{
		{
			Role:    schema.System,
			Content: systemPrompt,
		},
		{
			Role:    schema.User,
			Content: userInput,
		},
	}
}

// HistoryTurn 是前端带来的历史对话片段（user/assistant 配对），用于支撑追问语境。
type HistoryTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// BuildMessagesWithHistory 在 BuildMessages 基础上，把历史多轮对话前置到本轮用户输入之前。
func BuildMessagesWithHistory(systemPrompt string, history []HistoryTurn, userInput string) []*schema.Message {
	msgs := []*schema.Message{{Role: schema.System, Content: systemPrompt}}
	for _, h := range history {
		role := schema.User
		if h.Role == "assistant" {
			role = schema.Assistant
		}
		msgs = append(msgs, &schema.Message{Role: role, Content: h.Content})
	}
	msgs = append(msgs, &schema.Message{Role: schema.User, Content: userInput})
	return msgs
}
