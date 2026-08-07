// Package agent 实现 LLM 问股 agent（Stage 8）。
//
// 使用 eino 框架组装 ReAct agent，替代原版 Python 的 199KB 手写 agent 循环。
// 支持 Anthropic 和 OpenAI 双协议（通过 eino-ext 的 ChatModel 适配）。
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/jwcen/argent-go/internal/market"
	"github.com/jwcen/argent-go/internal/portfolio"
)

// Config LLM 配置。
type Config struct {
	Provider string // "anthropic" / "openai"
	APIKey   string
	BaseURL  string
	Model    string // 模型名
}

// LoadConfig 从环境变量加载 LLM 配置。
func LoadConfig() Config {
	return Config{
		Provider: envOr("ARGENT_LLM_PROVIDER", "openai"),
		APIKey:   os.Getenv("ARGENT_LLM_API_KEY"),
		BaseURL:  os.Getenv("ARGENT_LLM_BASE_URL"),
		Model:    envOr("ARGENT_LLM_MODEL", "gpt-4o-mini"),
	}
}

// Service 是 LLM agent 服务。
type Service struct {
	cfg    Config
	quoter market.Quoter
	kliner market.KlineProvider
	logger *slog.Logger
}

func NewService(cfg Config, quoter market.Quoter, kliner market.KlineProvider, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{cfg: cfg, quoter: quoter, kliner: kliner, logger: logger}
}

// Chat 处理一次非流式问答。
// Stage 8 简化版：直接调 ChatModel，不走 ReAct agent 循环。
// 后续可升级为 eino 的 flow/agent/react。
func (s *Service) Chat(ctx context.Context, messages []*schema.Message) (string, error) {
	chatModel, err := s.buildModel(ctx)
	if err != nil {
		return "", fmt.Errorf("agent: build model: %w", err)
	}

	resp, err := chatModel.Generate(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("agent: generate: %w", err)
	}
	return resp.Content, nil
}

// ChatStream 流式问答（SSE）。
// 返回一个 channel，逐块输出 LLM 的回复。
func (s *Service) ChatStream(ctx context.Context, messages []*schema.Message) (<-chan string, error) {
	chatModel, err := s.buildModel(ctx)
	if err != nil {
		return nil, fmt.Errorf("agent: build model: %w", err)
	}

	stream, err := chatModel.Stream(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("agent: stream: %w", err)
	}

	out := make(chan string, 64)
	go func() {
		defer close(out)
		for {
			msg, err := stream.Recv()
			if err != nil {
				return
			}
			if msg.Content != "" {
				out <- msg.Content
			}
		}
	}()
	return out, nil
}

// buildModel 根据配置创建 ChatModel。
// 使用 eino-ext 的 OpenAI / Anthropic 适配器。
func (s *Service) buildModel(ctx context.Context) (model.ChatModel, error) {
	if s.cfg.APIKey == "" {
		return nil, fmt.Errorf("agent: LLM API key not configured")
	}

	// 简化版：统一用 OpenAI 兼容接口（Anthropic 也支持 OpenAI 兼容 API）。
	// 后续可按 provider 分支用 eino-ext/components/model/{openai,claude}。
	return newOpenAIModel(ctx, s.cfg)
}

// PortfolioTools 把 portfolio service 的能力暴露给 agent。
// Stage 8 先实现核心 2 个工具：查持仓、查报价。
type PortfolioTools struct {
	portfolio *portfolio.Service
	quoter    market.Quoter
}

func NewPortfolioTools(p *portfolio.Service, q market.Quoter) *PortfolioTools {
	return &PortfolioTools{portfolio: p, quoter: q}
}

// GetHoldings 工具：返回当前持仓列表。
func (t *PortfolioTools) GetHoldings(ctx context.Context) (string, error) {
	holdings, err := t.portfolio.ListHoldings(ctx)
	if err != nil {
		return "", err
	}
	if len(holdings) == 0 {
		return "当前无持仓", nil
	}
	var result string
	for _, h := range holdings {
		result += fmt.Sprintf("%s(%s) %d股 成本%.2f\n", h.StockName, h.StockCode, h.Shares, h.CostPrice)
	}
	return result, nil
}

// GetQuote 工具：查实时报价。
func (t *PortfolioTools) GetQuote(ctx context.Context, code string) (string, error) {
	quotes, err := t.quoter.Quote(ctx, []string{code})
	if err != nil {
		return "", err
	}
	q, ok := quotes[code]
	if !ok {
		return fmt.Sprintf("未找到 %s 的行情数据", code), nil
	}
	return fmt.Sprintf("%s(%s) 现价%.2f 涨跌%.2f%%", q.StockName, q.StockCode, q.Price, q.ChangePct), nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
