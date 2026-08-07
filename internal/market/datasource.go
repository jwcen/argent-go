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

// Quoter 实时报价端口。
type Quoter interface {
	Quote(ctx context.Context, codes []string) (map[string]*Quote, error)
}

// KlineProvider 日 K 线端口。
type KlineProvider interface {
	Kline(ctx context.Context, code string, days int) ([]KlineDay, error)
}

// IndexProvider 大盘指数端口。
type IndexProvider interface {
	Indices(ctx context.Context) ([]IndexData, error)
}

// Quoter+KlineProvider 组合接口，供 cascade 使用。
type DataSource interface {
	Quoter
	KlineProvider
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
