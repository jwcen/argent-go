// Package market 是行情数据源层（Stage 5）。
//
// 架构铁律：业务域只依赖本包定义的接口（Quoter/KlineProvider），
// 不依赖具体的数据源（Sina/Eastmoney/Tencent）。
// 数据源实现是「端口适配器」，由 cascade 装饰器组合降级。
package market

import (
	"context"
	"time"
)

// Quote 实时报价。
type Quote struct {
	StockCode string  `json:"stock_code"`
	StockName string  `json:"stock_name"`
	Price     float64 `json:"price"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	PrevClose float64 `json:"prev_close"`
	Volume    float64 `json:"volume"`     // 股
	Amount    float64 `json:"amount"`     // 元
	ChangePct float64 `json:"change_pct"` // %
	Amplitude float64 `json:"amplitude"`  // %
}

// KlineDay 日 K 线一根。
type KlineDay struct {
	Date   string  `json:"date"`
	Open   float64 `json:"open"`
	Close  float64 `json:"close"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Volume float64 `json:"volume"`
	Amount float64 `json:"amount"`
}

// IndexData 大盘指数。
type IndexData struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	ChangePct float64 `json:"change_pct"`
}

// Board 一个板块（行业/概念）的快照。
type Board struct {
	Code         string  `json:"code"`          // 板块代码，如 BK1626
	Name         string  `json:"name"`          // 板块名
	ChangePct    float64 `json:"change_pct"`    // 板块涨跌幅 %
	MainNetInflow float64 `json:"main_net_inflow"` // 主力净流入（元）
}

// BoardStock 板块成分股一行。
type BoardStock struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	ChangePct float64 `json:"change_pct"`
}

// MarketBreadth 市场宽度（涨跌家数），用于研判市场情绪。
type MarketBreadth struct {
	UpCount     int     `json:"up_count"`     // 上涨家数
	DownCount   int     `json:"down_count"`   // 下跌家数
	FlatCount   int     `json:"flat_count"`   // 平盘家数
	LimitUp     int     `json:"limit_up"`     // 涨停家数
	LimitDown   int     `json:"limit_down"`   // 跌停家数
	UpDownRatio float64 `json:"up_down_ratio"` // 涨跌比 = 上涨/下跌
}

// ForeignIndex 海外/亚太指数一行。
type ForeignIndex struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	ChangePct float64 `json:"change_pct"`
}

// Quoter 实时报价端口。
type Quoter interface {
	Quote(ctx context.Context, codes []string) (map[string]*Quote, error)
}

// KlineProvider 日 K 线端口。
//
// period：0=日K(默认)，102=周K，103=月K（对应东财 klt 编码）。
// 非日K只能从东财取，其它源直接返回错误。
type KlineProvider interface {
	Kline(ctx context.Context, code string, period int, days int) ([]KlineDay, error)
}

// K 线周期常量。0=日K 默认，102=周K，103=月K。
const (
	PeriodDaily   = 0
	PeriodWeekly  = 102
	PeriodMonthly = 103
)

// IndexProvider 大盘指数端口。
type IndexProvider interface {
	Indices(ctx context.Context) ([]IndexData, error)
}

// SectorProvider 板块/市场宽度端口（行业/概念板块榜、板块成分、市场情绪）。
type SectorProvider interface {
	// Sectors 返回板块榜，kind 为 "industry"（行业）或 "concept"（概念），按涨跌幅降序取前 limit 个。
	Sectors(ctx context.Context, kind string, limit int) ([]Board, error)
	// BoardStocks 返回某板块的成分股（前 limit 只，按涨跌幅降序）。
	BoardStocks(ctx context.Context, boardCode string, limit int) ([]BoardStock, error)
	// MarketBreadth 市场宽度（涨跌家数/涨跌停）。
	MarketBreadth(ctx context.Context) (*MarketBreadth, error)
	// ForeignIndices 海外/亚太主要指数（道指/纳指/标普/恒生/日经等）。
	ForeignIndices(ctx context.Context) ([]ForeignIndex, error)
}

// Quoter+KlineProvider 组合接口，供 cascade 使用。
type DataSource interface {
	Quoter
	KlineProvider
}

// StockSuggest 搜索建议一行（用于输入框自动补全）。
type StockSuggest struct {
	Code   string `json:"code"`             // 6 位代码
	Name   string `json:"name"`             // 名称
	Pinyin string `json:"pinyin,omitempty"` // 拼音首字母（可选）
	Market string `json:"market"`           // SH/SZ/BJ；基金为空
	Type   string `json:"type"`             // STOCK / FUND
}

// Searcher 股票代码/名称模糊搜索端口。
// 用于「记一笔」弹窗的股票代码自动补全。
type Searcher interface {
	Search(ctx context.Context, keyword string, limit int) ([]StockSuggest, error)
}

// FundEstimate 场外基金盘中估值快照。
// 数据源：东财 Fund_JJJZ_Data.aspx（盘中实时，非交易时段估值为 0）。
type FundEstimate struct {
	Code              string  `json:"code"`
	Name              string  `json:"name"`
	UnitNAV           float64 `json:"unit_nav"`            // 官方单位净值
	DailyChangePct    float64 `json:"daily_change_pct"`    // 官方净值日增长率（%）
	EstimateNAV       float64 `json:"estimate_nav"`        // 盘中估算净值，非交易时段为 0
	EstimateChangePct float64 `json:"estimate_change_pct"` // 盘中估算增长率（%，已去掉 % 号）
}

// FundEstimator 场外基金盘中估值查询端口。
// 用于自选页的「实时估值」展示。
type FundEstimator interface {
	EstimateFunds(ctx context.Context, codes []string) (map[string]*FundEstimate, error)
}

// ---- 符号转换工具 ----

// SinaSymbol 把 6 位股票码转成新浪格式（sh/sz/bj 前缀）。
func SinaSymbol(code string) string {
	if len(code) != 6 {
		return code
	}
	if isBSE(code) {
		return "bj" + code
	}
	if code[0] == '6' || code[0] == '9' || code[0] == '5' {
		return "sh" + code
	}
	return "sz" + code
}

// EMSecid 把 6 位股票码转成东财 secid（1.code 沪 / 0.code 深北）。
func EMSecid(code string) string {
	if len(code) != 6 {
		return "0." + code
	}
	if isBSE(code) {
		return "0." + code
	}
	if code[0] == '6' || code[0] == '9' || code[0] == '5' {
		return "1." + code
	}
	return "0." + code
}

// TencentSymbol 腾讯格式：sh/sz/bj 前缀，同新浪。
func TencentSymbol(code string) string { return SinaSymbol(code) }

func isBSE(code string) bool {
	if len(code) < 3 {
		return false
	}
	return code[:3] == "920" || code[0] == '8' || code[0] == '4'
}

// IsAShare 判断是否 A 股代码（6 位数字）。
func IsAShare(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ---- 交易日历 ----

// IsTradingDay 判断给定日期是否为 A 股交易日。
// 简化版：周末不交易 + 硬编码节假日表。生产环境可改为读 JSON 文件。
func IsTradingDay(t time.Time) bool {
	// 周末
	wd := t.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	// 节假日（YYYYMMDD）
	dateStr := t.Format("20060102")
	if _, ok := holidays[dateStr]; ok {
		return false
	}
	return true
}

// NextTradingDay 返回 t 之后的下一个交易日。
func NextTradingDay(t time.Time) time.Time {
	d := t.AddDate(0, 0, 1)
	for !IsTradingDay(d) {
		d = d.AddDate(0, 0, 1)
	}
	return d
}
