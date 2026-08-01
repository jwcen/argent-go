package ledger

import "math"

// 货币时间价值 / NPV 计算。
//
// 说明：economics 是“分析型比率”计算（利率、现值、IRR 可行性），本质上就是浮点数学，
// 与 ledger 的“金额累加”不同，这里直接用 float64 与原版 Python 测试的取值对齐（对拍用
// 容差比较），不强行套 Money。金额累加的精度纪律仍由 fifo.go 的 domain.Money 保证。

// RealCost 把“名义金额”按年化机会成本折算到 days 天后的“真实成本”。
// 公式：nominal × (1+annualRate)^(days/365)，日复利。
func RealCost(nominal float64, days int, annualRate float64) float64 {
	return nominal * math.Pow(1+annualRate, float64(days)/365.0)
}

// OpportunityCost 这些钱若拿去无风险理财、days 天后能多赚多少（没赚到的机会成本）。
func OpportunityCost(principal float64, days int, annualRate float64) float64 {
	return principal * (math.Pow(1+annualRate, float64(days)/365.0) - 1)
}

// DailyOpportunityCost 每天的固定机会成本（单利日值：principal × rate / 365）。
func DailyOpportunityCost(principal, annualRate float64) float64 {
	return principal * annualRate / 365.0
}

// RequiredExitPrice 想在 yearsToExit 年后“至少不亏机会成本”地解套，卖出价需达到多少。
// 公式：entry × (1+annualRate)^yearsToExit。
func RequiredExitPrice(entry float64, yearsToExit, annualRate float64) float64 {
	return entry * math.Pow(1+annualRate, yearsToExit)
}

// HoldVsCut 持有 vs 割肉的 NPV 对比结论。
type HoldVsCut struct {
	Recommendation string  // "hold" 或 "cut"
	CutFV          float64 // 割肉后按指数收益再投的未来价值
	HoldFV         float64 // 按概率期望的回本未来价值
}

// HoldVsCutNPV 解套决策：比较“现在割肉、本金按指数年化再投”与“死扛到回本”的期望未来价值。
//
//   - CutFV  = currentValue × (1+indexAnnualReturn)^holdingYears  （落袋为安再投资）
//   - HoldFV = expectedRecoveryValue × recoveryProbability         （按概率打折的回本期望）
//   - 当 HoldFV ≥ CutFV 时建议 hold，否则 cut。
func HoldVsCutNPV(currentValue, expectedRecoveryValue, recoveryProbability, holdingYears, indexAnnualReturn float64) HoldVsCut {
	cutFV := currentValue * math.Pow(1+indexAnnualReturn, holdingYears)
	holdFV := expectedRecoveryValue * recoveryProbability
	rec := "cut"
	if holdFV >= cutFV {
		rec = "hold"
	}
	return HoldVsCut{Recommendation: rec, CutFV: cutFV, HoldFV: holdFV}
}
