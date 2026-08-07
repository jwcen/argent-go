package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TencentSource 腾讯财经（ifzq.gtimg.cn），前复权日K，抗东财 push2his 抽风。
type TencentSource struct {
	client *http.Client
}

func NewTencentSource() *TencentSource {
	return &TencentSource{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// 腾讯日K响应结构。
type tencentKlineResp struct {
	Data struct {
		QfqDay [][]any `json:"qfqday"` // 前复权
		Day    [][]any `json:"day"`    // 不复权
	} `json:"data"`
}

func (t *TencentSource) Kline(ctx context.Context, code string, days int) ([]KlineDay, error) {
	if !IsAShare(code) {
		return nil, nil
	}
	if days <= 0 {
		days = 60
	}

	symbol := TencentSymbol(code)
	// param: sh600519, day, , qfq, 60
	url := fmt.Sprintf("https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=%s,day,,,qfq,%d", symbol, days)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tencent kline: %w", err)
	}
	defer resp.Body.Close()

	var r tencentKlineResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("tencent kline: decode: %w", err)
	}

	rows := r.Data.QfqDay
	if len(rows) == 0 {
		rows = r.Data.Day
	}

	out := make([]KlineDay, 0, len(rows))
	for _, p := range rows {
		if len(p) < 6 {
			continue
		}
		kd := KlineDay{
			Date:   toString(p[0]),
			Open:   toFloat(p[1]),
			Close:  toFloat(p[2]),
			High:   toFloat(p[3]),
			Low:    toFloat(p[4]),
			Volume: toFloat(p[5]),
		}
		if len(p) > 6 {
			kd.Amount = toFloat(p[6])
		}
		out = append(out, kd)
	}
	return out, nil
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case string:
		return parseFloat(strings.TrimSpace(n))
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
