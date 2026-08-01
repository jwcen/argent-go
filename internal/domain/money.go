// Package domain 存放跨域共享的“内核”：金额、实体、错误。
// 这里只放零依赖的纯类型，业务域与基础设施层都能复用，且永不 import gin/sqlite/eino。
package domain

import "math"

// Money 以「分」为单位的整数金额。
//
// 为什么不用 float64 表示钱：A 股价格/金额用浮点累加会出现 0.1+0.2≠0.3 这类
// 二进制小数误差，天长日久在“成本/盈亏”上会漂移。统一用 int64 分做所有加减乘除，
// 只在“展示/与外部契约”边界才转成元（Yuan）。这是与原 Python 版（float 价格）最大的
// 一个架构差异点。
type Money int64

// Yuan 把“元”转成 Money（分），四舍五入到分。
func Yuan(y float64) Money {
	return Money(math.Round(y * 100))
}

// YuanF 把 Money 转回“元”（浮点，仅用于展示/对拍断言）。
func (m Money) YuanF() float64 {
	return float64(m) / 100
}

func (m Money) Add(o Money) Money    { return m + o }
func (m Money) Sub(o Money) Money    { return m - o }
func (m Money) MulInt(n int64) Money { return m * Money(n) }
func (m Money) IsZero() bool         { return m == 0 }

// String 给人看的“¥12.34”格式。
func (m Money) String() string {
	sign := ""
	v := int64(m)
	if v < 0 {
		sign = "-"
		v = -v
	}
	return sign + "¥" + fmtInt(int(v/100)) + "." + fmt2(int(v%100))
}

func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func fmt2(n int) string {
	if n < 10 {
		return "0" + string(byte('0'+n))
	}
	return string(byte('0'+n/10)) + string(byte('0'+n%10))
}
