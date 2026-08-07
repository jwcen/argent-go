package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// EastmoneySource 东方财富日K（push2his），前复权。
//
// API: https://push2his.eastmoney.com/api/qt/stock/kline/get
// 参数: secid=1.600519 & klt=101(日K) & fqt=1(前复权) & lmt=天数
// 反爬：必须带 Referer: https://quote.eastmoney.com/
type EastmoneySource struct {
	client *http.Client
}

func NewEastmoneySource() *EastmoneySource {
	return &EastmoneySource{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// 东财日K响应结构。
type emKlineResp struct {
	Data *struct {
		Klines []string `json:"klines"`
	} `json:"data"`
}

func (e *EastmoneySource) Kline(ctx context.Context, code string, days int) ([]KlineDay, error) {
	if !IsAShare(code) {
		return nil, nil
	}
	if days <= 0 {
		days = 60
	}

	secid := EMSecid(code)
	hosts := []string{"push2his.eastmoney.com", "push2.eastmoney.com"}

	var lastErr error
	for _, host := range hosts {
		url := fmt.Sprintf("https://%s/api/qt/stock/kline/get?secid=%s&fields1=f1&fields2=f51,f52,f53,f54,f55,f56,f57&klt=101&fqt=1&end=20500101&lmt=%d",
			host, secid, days)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Referer", "https://quote.eastmoney.com/")

		resp, err := e.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		var r emKlineResp
		err = json.NewDecoder(resp.Body).Decode(&r)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		if r.Data == nil || len(r.Data.Klines) == 0 {
			lastErr = fmt.Errorf("eastmoney: empty klines")
			continue
		}

		return parseEMKlines(r.Data.Klines), nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("eastmoney: all hosts failed: %w", lastErr)
	}
	return nil, nil
}

func parseEMKlines(klines []string) []KlineDay {
	out := make([]KlineDay, 0, len(klines))
	for _, ln := range klines {
		p := strings.Split(ln, ",")
		if len(p) < 6 {
			continue
		}
		kd := KlineDay{
			Date:   p[0],
			Open:   parseFloat(p[1]),
			Close:  parseFloat(p[2]),
			High:   parseFloat(p[3]),
			Low:    parseFloat(p[4]),
			Volume: parseFloat(p[5]),
		}
		if len(p) > 6 {
			kd.Amount = parseFloat(p[6])
		}
		out = append(out, kd)
	}
	return out
}

// EastmoneySource 也实现 Quoter（用 push2 实时报价）。
func (e *EastmoneySource) Quote(ctx context.Context, codes []string) (map[string]*Quote, error) {
	if len(codes) == 0 {
		return map[string]*Quote{}, nil
	}

	// 东财批量报价：secids=1.600519,0.000001
	secids := make([]string, 0, len(codes))
	for _, c := range codes {
		if IsAShare(c) {
			secids = append(secids, EMSecid(c))
		}
	}
	if len(secids) == 0 {
		return map[string]*Quote{}, nil
	}

	url := fmt.Sprintf("https://push2.eastmoney.com/api/qt/ulist.np/get?fields=f1,f2,f3,f4,f5,f6,f12,f14&secids=%s",
		strings.Join(secids, ","))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://quote.eastmoney.com/")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eastmoney quote: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return parseEMQuote(body), nil
}

type emQuoteResp struct {
	Data *struct {
		Diff []struct {
			Code  int     `json:"f12"` // 代码
			Name  string  `json:"f14"` // 名称
			Price float64 `json:"f2"`  // 最新价
			Chg   float64 `json:"f3"`  // 涨跌幅
			High  float64 `json:"f4"`
			Low   float64 `json:"f5"`
			Open  float64 `json:"f46"`
			Vol   float64 `json:"f5"`
		} `json:"diff"`
	} `json:"data"`
}

func parseEMQuote(body []byte) map[string]*Quote {
	var r emQuoteResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil
	}
	result := make(map[string]*Quote)
	if r.Data == nil {
		return result
	}
	for _, d := range r.Data.Diff {
		code := fmt.Sprintf("%d", d.Code)
		result[code] = &Quote{
			StockCode: code,
			StockName: d.Name,
			Price:     d.Price / 100,
			Open:      d.Open / 100,
			High:      d.High / 100,
			Low:       d.Low / 100,
			ChangePct: d.Chg / 100,
			Volume:    d.Vol,
		}
	}
	return result
}

// Indices 大盘指数。
func (e *EastmoneySource) Indices(ctx context.Context) ([]IndexData, error) {
	// 上证指数 1.000001, 深证成指 0.399001, 创业板指 0.399006, 沪深300 1.000300
	secids := "1.000001,0.399001,0.399006,1.000300"
	url := fmt.Sprintf("https://push2.eastmoney.com/api/qt/ulist.np/get?fields=f2,f3,f12,f14&secids=%s", secids)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://quote.eastmoney.com/")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eastmoney indices: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var r emQuoteResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	if r.Data == nil {
		return nil, nil
	}

	out := make([]IndexData, 0, len(r.Data.Diff))
	for _, d := range r.Data.Diff {
		out = append(out, IndexData{
			Code:      fmt.Sprintf("%d", d.Code),
			Name:      d.Name,
			Price:     d.Price / 100,
			ChangePct: d.Chg / 100,
		})
	}
	return out, nil
}
