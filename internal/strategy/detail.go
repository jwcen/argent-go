package strategy

import (
	"fmt"

	"github.com/jwcen/argent-go/internal/market"
)

// TechnicalDetail 单只个股的技术面明细，供详情页 K 线图 + 指标卡片渲染。
//
// 与 Report（快照 + 中性信号 + 决策复盘）互补：Report 是「结论」，
// 这里给「过程」——完整序列，让前端能画出均线、布林带、MACD 柱。
type TechnicalDetail struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	LastClose float64 `json:"last_close"`

	// 序列（与 Klines 等长，对齐下标）
	Klines  []KlineBar    `json:"klines"`
	MA5     []float64     `json:"ma5"`
	MA10    []float64     `json:"ma10"`
	MA20    []float64     `json:"ma20"`
	MA60    []float64     `json:"ma60"`
	BollUp  []float64     `json:"boll_up"`
	BollMid []float64     `json:"boll_mid"`
	BollLow []float64     `json:"boll_low"`
	MACDDif []float64     `json:"macd_dif"`
	MACDDea []float64     `json:"macd_dea"`
	MACDHist []float64    `json:"macd_hist"`

	// 支撑 / 压力位（近期高低点，中性参考）
	Support     float64 `json:"support"`     // 近端支撑（20 日低点）
	Resistance  float64 `json:"resistance"`  // 近端压力（20 日高点）
	SupportFar  float64 `json:"support_far"` // 远端支撑（60 日低点）
	ResistanceFar float64 `json:"resistance_far"` // 远端压力（60 日高点）
}

// KlineBar 一根 K 线（供前端画蜡烛图）。
type KlineBar struct {
	Date   string  `json:"date"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}

// AnalyzeTechnical 计算单只个股的完整技术面明细。纯函数。
func AnalyzeTechnical(code, name string, klines []market.KlineDay) (*TechnicalDetail, error) {
	if len(klines) < 30 {
		return nil, fmt.Errorf("k线数据不足（至少需 30 根）")
	}
	c := closesOf(klines)

	up, mid, low := Bollinger(c, 20, 2)
	dif, dea, hist := MACD(c)

	d := &TechnicalDetail{
		Code:      code,
		Name:      name,
		LastClose: c[len(c)-1],
		Klines:    make([]KlineBar, len(klines)),
		MA5:       SMA(c, 5),
		MA10:      SMA(c, 10),
		MA20:      SMA(c, 20),
		MA60:      SMA(c, 60),
		BollUp:    up,
		BollMid:   mid,
		BollLow:   low,
		MACDDif:   dif,
		MACDDea:   dea,
		MACDHist:  hist,
	}
	for i, k := range klines {
		d.Klines[i] = KlineBar{Date: k.Date, Open: k.Open, High: k.High, Low: k.Low, Close: k.Close, Volume: k.Volume}
	}
	d.Support, d.Resistance = minMax(klines, 20)
	d.SupportFar, d.ResistanceFar = minMax(klines, 60)
	return d, nil
}

// minMax 取最近 n 根 K 线的低点（支撑）与高点（压力）。
func minMax(klines []market.KlineDay, n int) (lo, hi float64) {
	if n > len(klines) {
		n = len(klines)
	}
	if n <= 0 {
		return 0, 0
	}
	lo = klines[len(klines)-1].Low
	hi = klines[len(klines)-1].High
	for i := len(klines) - n; i < len(klines); i++ {
		if klines[i].Low < lo {
			lo = klines[i].Low
		}
		if klines[i].High > hi {
			hi = klines[i].High
		}
	}
	return lo, hi
}
