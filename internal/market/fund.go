package market

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// FundQuote 场外基金净值快照。
//
// 与 A 股 Quote 的差异：基金按「净值 × 份额」计价，没有开高低收，
// 关键字段是单位净值（申购/赎回的计价基准）与累计净值（含分红再投）。
type FundQuote struct {
	Code      string  `json:"code"`       // 6 位基金代码，如 110011
	Name      string  `json:"name"`       // 基金全称（含基金类型后缀，如 易方达蓝筹精选混合）
	UnitNAV   float64 `json:"unit_nav"`   // 单位净值（最新）
	CumNAV    float64 `json:"cum_nav"`    // 累计净值
	PrevNAV   float64 `json:"prev_nav"`   // 前一日单位净值
	Date      string  `json:"date"`       // 净值日期 YYYY-MM-DD
	ChangePct float64 `json:"change_pct"` // 当日涨跌幅 %
}

// FundQuoter 基金净值查询端口。
//
// 独立于 Quoter（A 股报价）：新浪基金接口用 f_ 前缀（f_110011），
// 字段格式与 A 股完全不同，不能混用。
type FundQuoter interface {
	QuoteFunds(ctx context.Context, codes []string) (map[string]*FundQuote, error)
}

// QuoteFunds 查询一批场外基金的净值（新浪基金接口，f_ 前缀）。
//
// 返回 map[code]*FundQuote，查不到的 code 不出现。数据源不可达时返回错误，
// 由上层（handler）降级为空数据。
func (s *SinaSource) QuoteFunds(ctx context.Context, codes []string) (map[string]*FundQuote, error) {
	if len(codes) == 0 {
		return map[string]*FundQuote{}, nil
	}

	symbols := make([]string, 0, len(codes))
	for _, c := range codes {
		if len(c) == 6 && isDigits(c) {
			symbols = append(symbols, "f_"+c)
		}
	}
	if len(symbols) == 0 {
		return map[string]*FundQuote{}, nil
	}

	url := "https://hq.sinajs.cn/list=" + strings.Join(symbols, ",")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://finance.sina.com.cn")

	client := s.client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sina fund: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("sina fund: read body: %w", err)
	}

	return parseFundResponse(gbkToUTF8(body)), nil
}

// parseFundResponse 解析新浪基金响应。
//
// 新浪基金行格式（f_ 前缀）：
//
//	var hq_str_f_110011="易方达优质精选混合(QDII),4.2438,6.0338,4.1945,2026-08-10,17.0448";
//
// 字段：名称, 单位净值, 累计净值, 前一日单位净值, 净值日期, ...（后续字段省略）。
func parseFundResponse(text string) map[string]*FundQuote {
	result := make(map[string]*FundQuote)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := sinaLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		symbol := m[1]
		if !strings.HasPrefix(symbol, "f_") {
			continue
		}
		dataStr := m[2]
		if dataStr == "" {
			continue
		}
		fields := strings.Split(dataStr, ",")
		if len(fields) < 5 {
			continue
		}
		code := strings.TrimPrefix(symbol, "f_")
		fq := &FundQuote{
			Code:    code,
			Name:    fields[0],
			UnitNAV: parseFloat(fields[1]),
			CumNAV:  parseFloat(fields[2]),
			PrevNAV: parseFloat(fields[3]),
			Date:    fields[4],
		}
		if fq.PrevNAV > 0 && fq.UnitNAV > 0 {
			fq.ChangePct = round2((fq.UnitNAV - fq.PrevNAV) / fq.PrevNAV * 100)
		}
		result[code] = fq
	}
	return result
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
