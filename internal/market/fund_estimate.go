package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 东方财富全市场基金估值接口（盘中实时，9:30-15:00；非交易时段 gsz=0）。
// 单次请求返回全部 ~8000 只基金的估值快照，字段在 datas 数组的固定索引上。
// 数据源：https://fund.eastmoney.com/Data/Fund_JJJZ_Data.aspx
const fundBulkURL = "https://fund.eastmoney.com/Data/Fund_JJJZ_Data.aspx?type=&page=1,8000&jsObj=Data"

// 字段索引（由抓包确认，参见项目文档）
const (
	idxCode           = 0
	idxName           = 1
	idxUnitNAV        = 3  // 单位净值
	idxDailyChangePct = 8  // 日增长率（%）
	idxEstimateNAV    = 16 // 盘中估算净值，非交易时段为 "0"
	idxEstimatePctRaw = 17 // 盘中估算增长率（带 %，如 "0.45%"）
)

// EastmoneyFundEstimateSource 通过「全市场一次性拉取」实现盘中估值。
// 单次请求覆盖所有场内代码，本地缓存 60s，期间所有请求都走内存。
// 比「按 code 逐个调东财单基金接口」简单得多，且对个人单用户负载完全够用。
type EastmoneyFundEstimateSource struct {
	client *http.Client

	mu     sync.RWMutex
	cache  map[string]*FundEstimate
	expiry time.Time
	ttl    time.Duration
}

func NewEastmoneyFundEstimateSource() *EastmoneyFundEstimateSource {
	return &EastmoneyFundEstimateSource{
		client: &http.Client{Timeout: 10 * time.Second},
		ttl:    60 * time.Second,
		cache:  map[string]*FundEstimate{},
	}
}

// EstimateFunds 返回请求代码对应的估值；查不到的 code 不出现在结果中。
// 首次或缓存过期时触发全量刷新。
func (s *EastmoneyFundEstimateSource) EstimateFunds(ctx context.Context, codes []string) (map[string]*FundEstimate, error) {
	if err := s.refresh(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*FundEstimate, len(codes))
	for _, c := range codes {
		if e, ok := s.cache[c]; ok {
			out[c] = e
		}
	}
	return out, nil
}

func (s *EastmoneyFundEstimateSource) refresh(ctx context.Context) error {
	s.mu.RLock()
	fresh := time.Now().Before(s.expiry) && len(s.cache) > 0
	s.mu.RUnlock()
	if fresh {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fundBulkURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Referer", "https://fund.eastmoney.com/")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("fund estimate: request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("fund estimate: read body: %w", err)
	}

	parsed, err := parseFundBulkEstimate(body)
	if err != nil {
		return fmt.Errorf("fund estimate: parse failed: %w", err)
	}

	s.mu.Lock()
	s.cache = parsed
	s.expiry = time.Now().Add(s.ttl)
	s.mu.Unlock()
	return nil
}

// parseFundBulkEstimate 解析 `var db={chars:[...],datas:[[...],...]}`。
// datas 每个元素都是字符串数组（数字也以字符串形式出现）。
func parseFundBulkEstimate(body []byte) (map[string]*FundEstimate, error) {
	s := string(body)
	loc := regexp.MustCompile(`\bdatas\s*:\s*\[`).FindStringIndex(s)
	if loc == nil {
		return nil, fmt.Errorf("no datas array in response (head=%q)", trimHead(s, 80))
	}
	start := loc[1] - 1 // the opening [
	depth := 0
	end := start
	for j := start; j < len(s); j++ {
		switch s[j] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				end = j + 1
			}
		}
		if end > start {
			break
		}
	}
	if end <= start {
		return nil, fmt.Errorf("datas array not closed")
	}

	var rows [][]json.RawMessage
	if err := json.Unmarshal([]byte(s[start:end]), &rows); err != nil {
		return nil, fmt.Errorf("datas json unmarshal: %w", err)
	}

	out := make(map[string]*FundEstimate, len(rows))
	for _, r := range rows {
		if len(r) <= idxEstimatePctRaw {
			continue
		}
		var code, name string
		_ = json.Unmarshal(r[idxCode], &code)
		_ = json.Unmarshal(r[idxName], &name)
		if code == "" {
			continue
		}
		out[code] = &FundEstimate{
			Code:              code,
			Name:              name,
			UnitNAV:           parseFloatField(r[idxUnitNAV]),
			DailyChangePct:    parseFloatField(r[idxDailyChangePct]),
			EstimateNAV:       parseFloatField(r[idxEstimateNAV]),
			EstimateChangePct: parseFloatField(stripPercent(r[idxEstimatePctRaw])),
		}
	}
	return out, nil
}

// parseFloatField 把 JSON 字符串（数字也以字符串形式给出）解析成 float64。
func parseFloatField(raw json.RawMessage) float64 {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

// stripPercent 去掉 "0.45%" 末尾的 %，返回 "0.45"。
func stripPercent(raw json.RawMessage) json.RawMessage {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return raw
	}
	return json.RawMessage(strings.TrimSuffix(strings.TrimSpace(s), "%"))
}

func trimHead(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}