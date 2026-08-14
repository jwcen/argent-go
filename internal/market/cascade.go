package market

import (
	"context"
	"fmt"
	"log/slog"
)

// Cascade 装饰器：主源失败自动降级到备选源。
//
// 这是装饰器模式（Decorator Pattern）的典型应用：
// Cascade 和被包装的 DataSource 实现同一个接口，
// 调用方完全不知道背后有多个源在尝试。
type Cascade struct {
	primary  DataSource // 主源（东财）
	fallback DataSource // 备选源（新浪）
	logger   *slog.Logger
}

func NewCascade(primary, fallback DataSource, logger *slog.Logger) *Cascade {
	if logger == nil {
		logger = slog.Default()
	}
	return &Cascade{primary: primary, fallback: fallback, logger: logger}
}

// Quote 先试主源，失败降级到备选。
func (c *Cascade) Quote(ctx context.Context, codes []string) (map[string]*Quote, error) {
	result, err := c.primary.Quote(ctx, codes)
	if err == nil && len(result) > 0 {
		return result, nil
	}
	if err != nil {
		c.logger.Debug("cascade: primary quote failed, trying fallback", "err", err)
	}
	return c.fallback.Quote(ctx, codes)
}

// Kline 先试主源（东财前复权），失败降级到腾讯（也是前复权），最后新浪（不复权兜底）。
func (c *Cascade) Kline(ctx context.Context, code string, days int) ([]KlineDay, error) {
	kl, err := c.primary.Kline(ctx, code, days)
	if err == nil && len(kl) > 0 {
		return kl, nil
	}
	if err != nil {
		c.logger.Debug("cascade: primary kline failed, trying fallback", "err", err, "code", code)
	}
	return c.fallback.Kline(ctx, code, days)
}

// Indices 大盘主要指数：主源（东财）支持则优先，否则试备选源（若也实现了 IndexProvider）。
// DataSource 接口本身不含 Indices，故用类型断言做能力探测，不强制所有源都实现。
func (c *Cascade) Indices(ctx context.Context) ([]IndexData, error) {
	if p, ok := c.primary.(IndexProvider); ok {
		if idx, err := p.Indices(ctx); err == nil && len(idx) > 0 {
			return idx, nil
		}
	}
	if fb, ok := c.fallback.(IndexProvider); ok {
		return fb.Indices(ctx)
	}
	return nil, fmt.Errorf("market: 无可用的大盘指数数据源")
}
