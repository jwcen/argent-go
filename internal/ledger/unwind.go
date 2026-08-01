package ledger

import (
	"math"
	"sort"
)

// 解套规划器：把一笔“解套预算”分配到若干被套股票，并为单只股票生成“分批买入档位”。

// ComputePriority 解套优先级。
//
// 设计（cleanroom：原版只给了相对单调性，公式由我们定）：
//   - 套得越深（costGapPct 越大）→ 优先级越高
//   - 处于下行趋势（trendStrength 越大）→ 优先级越低（别在下跌途中接飞刀）
//   - 基本面越好（fundamentalScore）→ 越高；波动率越高（volatilityRatio）→ 越低
//
// 返回越大越优先。
func ComputePriority(costGapPct, fundamentalScore, volatilityRatio, trendStrength float64) float64 {
	return costGapPct * (1 - trendStrength) * (1 + fundamentalScore) / (1 + 5*volatilityRatio)
}

// StockPriority 一只待解套股票及其优先级。
type StockPriority struct {
	StockCode string
	Priority  float64
}

// AllocateBudgets 按优先级比例分配总预算。
// 若总优先级为 0（全部 priority=0），则均分到每只股票。
func AllocateBudgets(stocks []StockPriority, totalBudget float64) map[string]float64 {
	total := 0.0
	for _, s := range stocks {
		total += s.Priority
	}
	res := make(map[string]float64, len(stocks))
	if total == 0 {
		per := totalBudget / float64(len(stocks))
		for _, s := range stocks {
			res[s.StockCode] = per
		}
		return res
	}
	for _, s := range stocks {
		res[s.StockCode] = totalBudget * s.Priority / total
	}
	return res
}

// Tranche 一个解套买入档位。
type Tranche struct {
	Idx            int     // 档位序号（0 = 最靠近现价）
	TriggerPrice   float64 // 触发买入价（< 现价）
	Shares         int64   // 该档买入股数
	RequiresHealth string  // 买入前需要的市场健康度："any" | "yellow" | "green"
}

// GenerateTranches 由现价向下、结合支撑位/下轨/历史低点，生成金字塔式分批买入档位。
//
// 规则：
//   - 候选触发价 = {现价-ATR, 各支撑位, 下轨, 历史低点}，过滤掉 ≥ 现价的并去重，按从近到远排序。
//   - 预算均分，每档 shares = 档位预算 / 触发价 → 越深的档位价格越低、股数越多（金字塔）。
//   - 越深的档位要求越严的健康确认：第 0 档 "any"，最末档 "green"，中间 "yellow"。
func GenerateTranches(currentPrice, atr float64, supports []float64, lowerBB, historicalLow, budget float64) []Tranche {
	candidates := []float64{currentPrice - atr}
	candidates = append(candidates, supports...)
	candidates = append(candidates, lowerBB, historicalLow)

	var prices []float64
	for _, p := range candidates {
		if p < currentPrice {
			prices = append(prices, p)
		}
	}
	sort.Float64s(prices)
	// 去重（相邻相等）
	var dedup []float64
	for i, p := range prices {
		if i > 0 && math.Abs(p-prices[i-1]) < 1e-9 {
			continue
		}
		dedup = append(dedup, p)
	}
	// 反转：从近（高）到远（低）
	for i, j := 0, len(dedup)-1; i < j; i, j = i+1, j-1 {
		dedup[i], dedup[j] = dedup[j], dedup[i]
	}

	n := len(dedup)
	if n == 0 {
		return nil
	}
	slice := budget / float64(n)
	tranches := make([]Tranche, n)
	for i, p := range dedup {
		health := "yellow"
		if i == 0 {
			health = "any"
		}
		if i == n-1 {
			health = "green"
		}
		tranches[i] = Tranche{
			Idx:            i,
			TriggerPrice:   p,
			Shares:         int64(slice / p),
			RequiresHealth: health,
		}
	}
	return tranches
}

// Feasibility 解套一档是否“划算”的结论。
type Feasibility struct {
	Feasible      bool    // 解套后成本在耐心期内能否被三年高点覆盖
	RequiredPrice float64 // 解套后需要达到的卖出价（new_cost × (1+r)^years）
	NewCost       float64 // 加仓后的综合成本
}

// CheckTrancheFeasibility 判断“在现持仓基础上再买一笔”解套是否可行：
// 加仓后综合成本 new_cost，耐心 patienceYears 年内按无风险利率增长到 required_price，
// 若 required_price ≤ 三年最高价，则届时能解套，feasible=true。
func CheckTrancheFeasibility(oldShares int64, oldCost, addPrice float64, addShares int64, historicalHigh3y, patienceYears, riskFreeRate float64) Feasibility {
	totalShares := float64(oldShares + addShares)
	newCost := (float64(oldShares)*oldCost + float64(addShares)*addPrice) / totalShares
	required := newCost * math.Pow(1+riskFreeRate, patienceYears)
	return Feasibility{
		Feasible:      required <= historicalHigh3y,
		RequiredPrice: required,
		NewCost:       newCost,
	}
}
