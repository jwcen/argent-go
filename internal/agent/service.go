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
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/jwcen/argent-go/internal/market"
	"github.com/jwcen/argent-go/internal/portfolio"
)

// Config LLM 配置。
type Config struct {
	Provider       string   // "anthropic" / "openai"
	APIKey         string
	BaseURL        string
	Model          string   // 主模型名
	FallbackModels []string // 备用模型轮换列表（仅从 ARGENT_LLM_FALLBACK_MODELS 环境变量读，不写进代码）
}

// LoadConfig 从环境变量加载 LLM 配置。
func LoadConfig() Config {
	// 备用模型列表：逗号分隔，仅来自环境变量，绝不硬编码在代码里。
	var fb []string
	if raw := os.Getenv("ARGENT_LLM_FALLBACK_MODELS"); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(p); t != "" {
				fb = append(fb, t)
			}
		}
	}
	return Config{
		Provider:       envOr("ARGENT_LLM_PROVIDER", "openai"),
		APIKey:         os.Getenv("ARGENT_LLM_API_KEY"),
		BaseURL:        os.Getenv("ARGENT_LLM_BASE_URL"),
		Model:          envOr("ARGENT_LLM_MODEL", "gpt-4o-mini"),
		FallbackModels: fb,
	}
}

// Service 是 LLM agent 服务。
type Service struct {
	cfg    Config
	quoter market.Quoter
	kliner market.KlineProvider
	fundQ  market.FundQuoter
	logger *slog.Logger
}

func NewService(cfg Config, quoter market.Quoter, kliner market.KlineProvider, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{cfg: cfg, quoter: quoter, kliner: kliner, logger: logger}
}

// SetFundQuoter 注入基金净值数据源（可选）。
func (s *Service) SetFundQuoter(q market.FundQuoter) {
	s.fundQ = q
}

// buildToolCallingModel 用指定模型名构造一个支持工具调用的 ChatModel。
// eino-ext 的 OpenAI 适配器返回的 *ChatModel 实现了 model.ToolCallingChatModel。
func (s *Service) buildToolCallingModel(ctx context.Context, modelName string) (model.ToolCallingChatModel, error) {
	if s.cfg.APIKey == "" {
		return nil, fmt.Errorf("agent: LLM API key not configured")
	}
	cfg := s.cfg
	cfg.Model = modelName
	m, err := newOpenAIModel(ctx, cfg)
	if err != nil {
		return nil, err
	}
	// newOpenAIModel 返回 model.ChatModel，但底层 *openai.ChatModel 实现了 ToolCallingChatModel。
	tcm, ok := m.(model.ToolCallingChatModel)
	if !ok {
		return nil, fmt.Errorf("agent: 模型 %s 不支持工具调用", modelName)
	}
	return tcm, nil
}

// Chat 处理一次非流式问答。
// 主模型不可用时（额度耗尽 / 凭证失效 / 模型下架等）自动切换到备用模型。
func (s *Service) Chat(ctx context.Context, messages []*schema.Message) (string, error) {
	var lastErr error
	for _, modelName := range s.orderedCandidates() {
		chatModel, err := s.buildModelWithName(ctx, modelName)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := chatModel.Generate(ctx, messages)
		if err != nil {
			if isExhaustionError(err) {
				lastErr = err
				s.logger.Warn("LLM 主模型不可用/额度耗尽，尝试下一个备用模型",
					"model", modelName, "error", err)
				continue
			}
			return "", fmt.Errorf("agent: generate: %w", err)
		}
		return resp.Content, nil
	}
	if lastErr != nil {
		return "", fmt.Errorf("agent: 所有模型均不可用，最后错误: %w", lastErr)
	}
	return "", fmt.Errorf("agent: 没有可用模型")
}

// ChatStream 流式问答（SSE）。
// 主模型不可用时自动切换备用模型；返回一个 channel，逐块输出 LLM 的回复。
func (s *Service) ChatStream(ctx context.Context, messages []*schema.Message) (<-chan string, error) {
	var lastErr error
	for _, modelName := range s.orderedCandidates() {
		chatModel, err := s.buildModelWithName(ctx, modelName)
		if err != nil {
			lastErr = err
			continue
		}
		stream, err := chatModel.Stream(ctx, messages)
		if err != nil {
			if isExhaustionError(err) {
				lastErr = err
				s.logger.Warn("LLM 主模型不可用/额度耗尽，尝试下一个备用模型",
					"model", modelName, "error", err)
				continue
			}
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
	if lastErr != nil {
		return nil, fmt.Errorf("agent: 所有模型均不可用，最后错误: %w", lastErr)
	}
	return nil, fmt.Errorf("agent: 没有可用模型")
}

// buildModel 根据配置创建主模型的 ChatModel（保留以兼容外部调用）。
func (s *Service) buildModel(ctx context.Context) (model.ChatModel, error) {
	return s.buildModelWithName(ctx, s.cfg.Model)
}

// buildModelWithName 用指定模型名创建 ChatModel（备用模型轮换时用）。
func (s *Service) buildModelWithName(ctx context.Context, modelName string) (model.ChatModel, error) {
	if s.cfg.APIKey == "" {
		return nil, fmt.Errorf("agent: LLM API key not configured")
	}
	cfg := s.cfg
	cfg.Model = modelName
	return newOpenAIModel(ctx, cfg)
}

// orderedCandidates 构造候选模型顺序：主模型优先，其次备用列表，去重保序。
func (s *Service) orderedCandidates() []string {
	seen := make(map[string]bool)
	var out []string
	add := func(m string) {
		if m != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	add(s.cfg.Model)
	for _, m := range s.cfg.FallbackModels {
		add(m)
	}
	return out
}

// isExhaustionError 判断该错误是否属于“换模型可能解决”的不可用/耗尽类错误。
// 与原版 Python llm_client._is_exhaustion_error 对齐：401/403/429 直接判，
// 其它通过关键字（quota/额度/key 无效/模型不存在等）识别，避免在代码里硬编码模型名。
func isExhaustionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "401") || strings.Contains(msg, "403") ||
		strings.Contains(msg, "429") || strings.Contains(msg, "forbidden") {
		return true
	}
	kw := []string{
		"quota", "额度", "余额", "exhaust", "insufficient",
		"invalid api key", "unauthorized", "api key",
		"model", "not found", "deprecat", "unavailable",
		"token", "expired", "used up",
	}
	for _, k := range kw {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
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

// IsConfigured 报告 LLM 是否已配置 API key。未配置时 AskStream 走本地演示降级。
// IsConfigured 判断 LLM 是否配置（有 API key）。
func (s *Service) IsConfigured() bool {
	return s.cfg.APIKey != ""
}

// ParseImage 把一张截图交给多模态 LLM 解析，返回模型的原始文本回复。
//
// imageB64 是 base64 编码的图片二进制；mimeType 形如 "image/png"。
// 模型是否支持视觉由配置决定；不支持时 Chat 会返回错误，由调用方兜底提示。
func (s *Service) ParseImage(ctx context.Context, imageB64, mimeType, systemPrompt, userPrompt string) (string, error) {
	msgs := []*schema.Message{
		{Role: schema.System, Content: systemPrompt},
		{
			Role: schema.User,
			UserInputMultiContent: []schema.MessageInputPart{
				{Type: schema.ChatMessagePartTypeText, Text: userPrompt},
				{
					Type: schema.ChatMessagePartTypeImageURL,
					Image: &schema.MessageInputImage{
						MessagePartCommon: schema.MessagePartCommon{
							Base64Data: &imageB64,
							MIMEType:   mimeType,
						},
						Detail: schema.ImageURLDetailHigh,
					},
				},
			},
		},
	}
	return s.Chat(ctx, msgs)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
