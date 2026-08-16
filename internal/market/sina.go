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
	"time"
)

// SinaSource 新浪财经实时报价。
//
// API: https://hq.sinajs.cn/list=sh600519,sz000001
// 反爬：必须带 Referer: https://finance.sina.com.cn
// 编码：GBK（A 股名称是中文）
type SinaSource struct {
	client *http.Client
}

func NewSinaSource() *SinaSource {
	return &SinaSource{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *SinaSource) Quote(ctx context.Context, codes []string) (map[string]*Quote, error) {
	if len(codes) == 0 {
		return map[string]*Quote{}, nil
	}

	symbols := make([]string, 0, len(codes))
	for _, c := range codes {
		if IsAShare(c) {
			symbols = append(symbols, SinaSymbol(c))
		}
	}
	if len(symbols) == 0 {
		return map[string]*Quote{}, nil
	}

	url := "https://hq.sinajs.cn/list=" + strings.Join(symbols, ",")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://finance.sina.com.cn")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sina: request failed: %w", err)
	}
	defer resp.Body.Close()

	// 新浪返回 GBK 编码，A 股名称是中文。Go 的 transform.Reader 需要引入 golang.org/x/text，
	// 但新浪的 A 股数据字段除了名称外都是 ASCII，所以我们读 raw bytes 后用 GBK 解码器处理。
	// 简化处理：直接按 Latin1 读取（中文名称会乱码但不影响数值），后续可用 mahonia/gbk 改进。
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("sina: read body: %w", err)
	}

	// 用 GBK 解码（简单实现：新浪 A 股中文部分用 gbk）
	text := gbkToUTF8(body)

	return parseSinaResponse(text), nil
}

var sinaLineRe = regexp.MustCompile(`var hq_str_(\w+)="(.*)";`)

func parseSinaResponse(text string) map[string]*Quote {
	result := make(map[string]*Quote)

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
		dataStr := m[2]
		if dataStr == "" {
			continue
		}

		fields := strings.Split(dataStr, ",")
		if len(fields) < 32 {
			continue
		}

		// symbol 格式：sh600519 / sz000001 / bj830799
		code := symbol[2:]

		name := fields[0]
		open := parseFloat(fields[1])
		prevClose := parseFloat(fields[2])
		price := parseFloat(fields[3])
		high := parseFloat(fields[4])
		low := parseFloat(fields[5])
		volume := parseFloat(fields[8])
		amount := parseFloat(fields[9])

		var changePct, amplitude float64
		if prevClose > 0 && price > 0 {
			changePct = round2((price - prevClose) / prevClose * 100)
		}
		if prevClose > 0 && high > 0 && low > 0 {
			amplitude = round2((high - low) / prevClose * 100)
		}

		result[code] = &Quote{
			StockCode: code,
			StockName: name,
			Price:     price,
			Open:      open,
			High:      high,
			Low:       low,
			PrevClose: prevClose,
			Volume:    volume,
			Amount:    amount,
			ChangePct: changePct,
			Amplitude: amplitude,
		}
	}
	return result
}

// SinaSource 也实现 KlineProvider（新浪不复权日K，ETF 拆分会断崖，仅作兜底）。
func (s *SinaSource) Kline(ctx context.Context, code string, period int, days int) ([]KlineDay, error) {
	if !IsAShare(code) {
		return nil, nil
	}
	// 新浪 scale：240=日K, 1200=周K, 7200=月K（分钟数换算）。
	scale := 240
	switch period {
	case PeriodWeekly:
		scale = 1200
	case PeriodMonthly:
		scale = 7200
	}
	symbol := SinaSymbol(code)
	url := fmt.Sprintf("https://money.finance.sina.com.cn/quotes_service/api/json_v2.php/CN_MarketData.getKLineData?symbol=%s&scale=%d&datalen=%d", symbol, scale, days)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://finance.sina.com.cn")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sina kline: %w", err)
	}
	defer resp.Body.Close()

	var raw []struct {
		Day   string `json:"day"`
		Open  string `json:"open"`
		High  string `json:"high"`
		Low   string `json:"low"`
		Close string `json:"close"`
		Vol   string `json:"volume"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("sina kline: decode: %w", err)
	}

	out := make([]KlineDay, 0, len(raw))
	for _, r := range raw {
		out = append(out, KlineDay{
			Date:   r.Day,
			Open:   parseFloat(r.Open),
			Close:  parseFloat(r.Close),
			High:   parseFloat(r.High),
			Low:    parseFloat(r.Low),
			Volume: parseFloat(r.Vol),
		})
	}
	return out, nil
}

func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// gbkToUTF8 简易 GBK→UTF8：新浪 A 股响应中中文用 GBK 编码。
// 使用 golang.org/x/text/encoding/simplifiedchinese 做转换。
func gbkToUTF8(b []byte) string {
	// 尝试用 golang.org/x/text 解码
	if decoded, err := decodeGBK(b); err == nil {
		return decoded
	}
	// fallback：按 latin1 读（名称会乱码但数值正常）
	return string(b)
}
