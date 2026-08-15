package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// EastmoneySource 东方财富日K（push2his），前复权。
//
// API: https://push2his.eastmoney.com/api/qt/stock/kline/get
// 参数: secid=1.600519 & klt=101(日K) & fqt=1(前复权) & lmt=天数
// 反爬：必须带 Referer: https://quote.eastmoney.com/

// fallbackRT 先走环境代理（东财 stock/get、clist 等接口需经代理出网），
// 代理失败（本机 ClashX 偶发抖动）时自动直连重试（ulist 等接口直连可用）。
type fallbackRT struct {
	primary   http.RoundTripper
	secondary http.RoundTripper
}

func (f *fallbackRT) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := f.primary.RoundTrip(req)
	if err != nil {
		return f.secondary.RoundTrip(req)
	}
	return resp, nil
}

type EastmoneySource struct {
	client *http.Client
}

func NewEastmoneySource() *EastmoneySource {
	proxyRT := &http.Transport{Proxy: http.ProxyFromEnvironment}
	directRT := &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return nil, nil }}
	return &EastmoneySource{
		client: &http.Client{
			Timeout:   15 * time.Second,
			Transport: &fallbackRT{primary: proxyRT, secondary: directRT},
		},
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

// indexSecids 大盘主要指数对应的东财 secid。
// 上证 1.000001 / 深成 0.399001 / 创业板 0.399006 / 沪深300 1.000300 / 科创50 1.000688 / 北证50 0.899050。
var indexSecids = []string{
	"1.000001", "0.399001", "0.399006", "1.000300", "1.000688", "0.899050",
}

// emIndexResp 是 stock/get?secid=X 的响应（单只，比 ulist 稳定）。
type emIndexResp struct {
	Data *struct {
		Code  string  `json:"f57"` // 代码，如 "000001"
		Name  string  `json:"f58"` // 名称
		Price float64 `json:"f43"` // 最新价（×100）
		Chg   float64 `json:"f170"` // 涨跌幅（×100）
	} `json:"data"`
}

// Indices 大盘主要指数实时涨跌。
// 用 stock/get?secid=X（逐只拉）而非 ulist 批量：本环境下 ulist 对指数 secid 常返回空，
// 而 stock/get 稳定可用。
func (e *EastmoneySource) Indices(ctx context.Context) ([]IndexData, error) {
	out := make([]IndexData, 0, len(indexSecids))
	var firstErr error
	for _, secid := range indexSecids {
		url := fmt.Sprintf("https://push2.eastmoney.com/api/qt/stock/get?secid=%s&fields=f43,f57,f58,f170", secid)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		req.Header.Set("Referer", "https://quote.eastmoney.com/")

		resp, err := e.client.Do(req)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		var r emIndexResp
		if err := json.Unmarshal(body, &r); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if r.Data == nil {
			continue
		}
		out = append(out, IndexData{
			Code:      r.Data.Code,
			Name:      r.Data.Name,
			Price:     r.Data.Price / 100,
			ChangePct: r.Data.Chg / 100,
		})
	}
	if len(out) == 0 && firstErr != nil {
		return nil, fmt.Errorf("eastmoney indices: %w", firstErr)
	}
	return out, nil
}

// ── 通用 stock/get 辅助 ──
// emStockGet 是 stock/get?secid=X 的通用响应，覆盖指数/海外指数/涨跌家数所需的字段。
type emStockGet struct {
	Data *struct {
		Code     string  `json:"f57"`  // 代码
		Name     string  `json:"f58"`  // 名称
		Price    float64 `json:"f43"`  // 最新价（×100）
		Chg      float64 `json:"f170"` // 涨跌幅（×100）
		Up       int     `json:"f104"` // 上涨家数
		Down     int     `json:"f105"` // 下跌家数
		Flat     int     `json:"f128"` // 平盘家数
		LimitUp  int     `json:"f136"` // 涨停家数
		LimitDn  int     `json:"f116"` // 跌停家数
	} `json:"data"`
}

// stockGet 拉取单个 secid 的指定字段。
func (e *EastmoneySource) stockGet(ctx context.Context, secid, fields string) (*emStockGet, error) {
	url := fmt.Sprintf("https://push2.eastmoney.com/api/qt/stock/get?secid=%s&fields=%s", secid, fields)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	var r emStockGet
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ── clist 辅助（板块榜 / 板块成分股）──
type emClistResp struct {
	Data *struct {
		Total int `json:"total"`
		Diff  []struct {
			Code  string  `json:"f12"`
			Name  string  `json:"f14"`
			Chg   float64 `json:"f3"`  // 涨跌幅（×100）
			NetIn float64 `json:"f62"` // 主力净流入（元）
		} `json:"diff"`
	} `json:"data"`
}

// clist 拉取板块榜/成分股（clist 接口），返回 diff 数组。
func (e *EastmoneySource) clist(ctx context.Context, fs, fields string, limit int) (*emClistResp, error) {
	url := fmt.Sprintf("https://push2.eastmoney.com/api/qt/clist/get?pn=1&pz=%d&po=1&np=1&fltt=2&invt=2&fid=f3&fs=%s&fields=%s",
		limit, fs, fields)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	var r emClistResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Sectors 板块榜：industry=行业板块(m:90+t:2)，concept=概念板块(m:90+t:3)，按涨跌幅降序。
func (e *EastmoneySource) Sectors(ctx context.Context, kind string, limit int) ([]Board, error) {
	fs := "m:90+t:2" // 行业
	if kind == "concept" {
		fs = "m:90+t:3" // 概念
	}
	if limit <= 0 {
		limit = 15
	}
	r, err := e.clist(ctx, fs, "f12,f14,f3,f62", limit)
	if err != nil {
		return nil, err
	}
	if r.Data == nil || len(r.Data.Diff) == 0 {
		return nil, nil
	}
	out := make([]Board, 0, len(r.Data.Diff))
	for _, d := range r.Data.Diff {
		out = append(out, Board{
			Code:          d.Code,
			Name:          d.Name,
			ChangePct:     round2(d.Chg / 100),
			MainNetInflow: d.NetIn,
		})
	}
	return out, nil
}

// BoardStocks 某板块的成分股（按涨跌幅降序）。
func (e *EastmoneySource) BoardStocks(ctx context.Context, boardCode string, limit int) ([]BoardStock, error) {
	boardCode = strings.TrimPrefix(boardCode, "BK")
	boardCode = "BK" + boardCode
	if limit <= 0 {
		limit = 15
	}
	r, err := e.clist(ctx, "b:"+boardCode, "f12,f14,f3", limit)
	if err != nil {
		return nil, err
	}
	if r.Data == nil || len(r.Data.Diff) == 0 {
		return nil, nil
	}
	out := make([]BoardStock, 0, len(r.Data.Diff))
	for _, d := range r.Data.Diff {
		out = append(out, BoardStock{
			Code:      d.Code,
			Name:      d.Name,
			ChangePct: round2(d.Chg / 100),
		})
	}
	return out, nil
}

// MarketBreadth 市场宽度：取上证指数(1.000001)的涨跌家数。
func (e *EastmoneySource) MarketBreadth(ctx context.Context) (*MarketBreadth, error) {
	r, err := e.stockGet(ctx, "1.000001", "f104,f105,f128,f136,f116")
	if err != nil {
		return nil, err
	}
	if r.Data == nil {
		return nil, nil
	}
	d := r.Data
	ratio := 0.0
	if d.Down > 0 {
		ratio = round2(float64(d.Up) / float64(d.Down))
	} else if d.Up > 0 {
		ratio = float64(d.Up) // 下跌为0，记为上涨家数本身
	}
	return &MarketBreadth{
		UpCount:     d.Up,
		DownCount:   d.Down,
		FlatCount:   d.Flat,
		LimitUp:     d.LimitUp,
		LimitDown:   d.LimitDn,
		UpDownRatio: ratio,
	}, nil
}

// foreignSecids 海外/亚太主要指数 secid。
var foreignSecids = []struct {
	secid string
	name  string
}{
	{"100.DJI", "道琼斯"},
	{"100.NDX", "纳斯达克"},
	{"100.SPX", "标普500"},
	{"100.HSI", "恒生指数"},
	{"100.N225", "日经225"},
}

// ── 搜索建议（自动补全）──

// emSearchResp 东财搜索建议响应。
type emSearchResp struct {
	QuotationCodeTable *struct {
		Data []struct {
			Code    string `json:"Code"`    // 6 位代码
			Name    string `json:"Name"`    // 名称
			Pinyin  string `json:"PyShort"` // 拼音首字母
			SecName string `json:"SecName"` // 完整名称（含后缀）
		} `json:"Data"`
	} `json:"QuotationCodeTable"`
}

// Search 股票代码/名称模糊搜索。
// 用东财搜索建议 API：https://searchapi.eastmoney.com/api/suggest/get
// type=14 表示 A 股股票，count 控制返回条数。
func (e *EastmoneySource) Search(ctx context.Context, keyword string, limit int) ([]StockSuggest, error) {
	if keyword == "" {
		return []StockSuggest{}, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 10
	}

	u := fmt.Sprintf(
		"https://searchapi.eastmoney.com/api/suggest/get?input=%s&type=14&token=D43BF722C8E33BDC906FB84D85E326E8&count=%d",
		url.QueryEscape(keyword), limit,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://quote.eastmoney.com/")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var r emSearchResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	if r.QuotationCodeTable == nil || r.QuotationCodeTable.Data == nil {
		return []StockSuggest{}, nil
	}

	out := make([]StockSuggest, 0, len(r.QuotationCodeTable.Data))
	for _, d := range r.QuotationCodeTable.Data {
		code := d.Code
		// 东财返回的 code 可能带前缀或非6位，只取纯数字部分
		if len(code) >= 6 && isAllDigit(code[:6]) {
			code = code[:6]
		}
		market := "SZ"
		if len(code) == 6 && (code[0] == '6' || code[0] == '9' || code[0] == '5') {
			market = "SH"
		}
		if len(code) == 6 && (code[0] == '8' || code[0] == '4') {
			market = "BJ"
		}
		out = append(out, StockSuggest{
			Code:   code,
			Name:   d.Name,
			Pinyin: d.Pinyin,
			Market: market,
		})
	}
	return out, nil
}

func isAllDigit(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
