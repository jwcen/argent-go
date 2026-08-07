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
