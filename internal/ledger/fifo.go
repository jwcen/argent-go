// Package ledger 是 Argent 的“纯计算内核”（Stage 1）。
//
// 设计立场（对标生产项目 + 简洁架构）：
//   - 这一层是“实体层”：纯函数、零 IO、不 import gin/sqlite/eino，可被单测/对拍直接调用。
//   - 所有金额走 domain.Money（int64 分），杜绝浮点累加误差。
//   - 行为契约来自 wiki/04 的“纯内部逻辑”清单 + 原版 Python 测试的取值（cleanroom：只看
//     测试断言值，不抄 services/ 实现逻辑）。
package ledger

import (
	"math"
	"sort"
	"time"

	"github.com/jwcen/argent-go/internal/domain"
)

// ActionType 持仓动作类型。A 股里 ADD（增股/红股入账等）与 BUY 都视作“建仓/加仓”。
type ActionType string

const (
	ActionBuy  ActionType = "BUY"
	ActionSell ActionType = "SELL"
	ActionAdd  ActionType = "ADD"
)

// Action 一笔持仓流水。
type Action struct {
	Type      ActionType
	Price     domain.Money // 每股价格（分）
	Shares    int64
	TradeDate time.Time
}

// Lot FIFO 账本里的一“批”持仓：某日买入的若干股，价格固定。
type Lot struct {
	Price     domain.Money
	Shares    int64
	TradeDate time.Time
}

// PositionState 一段（或当前）持仓的聚合结果。
type PositionState struct {
	Shares        int64        // 当前总股数
	CostPrice     domain.Money // 综合成本法单价（分）：(本轮买入额 - 本轮卖出额) / 当前股数
	FIFOCostPrice domain.Money // FIFO 成本法单价（分）：剩余批次按原始买入价加权
	WeightedDays  int          // 按股数加权的平均持有天数
	RealizedPnL   domain.Money // 累计已实现盈亏（含所有卖出，跨段累加）
	RealizedCarry domain.Money // 已清仓段的已实现盈亏（未摊进当前浮动成本）
	Lots          []Lot        // 剩余批次
}

// ComputePositionState 由一串（可能乱序的）持仓动作，算出当前聚合状态。
//
// 关键规则（来自测试契约）：
//  1. 输入先按 TradeDate 稳定排序（同日保持输入顺序，保证 BUY 先于 SELL）。
//  2. FIFO：卖出从“最旧批次”开始消耗，已实现 = (卖出价 - 批次买入价) × 股数。
//  3. 成本按“持仓段(episode)”重置：从 0 买入到卖回 0 是一段；清仓后重买，新成本
//     就是新买入价，不会把上一轮的盈亏摊进新成本（这是原版一个已知 bug 的修正点）。
//  4. realized_carry 只统计“已清仓段”的已实现；当前未平仓段的已实现仍计入
//     realized_pnl，但不进 carry（当前浮动成本已隐含这部分）。
//  5. weighted_days = Σ(批次股数 × 该批次持有天数) / 总股数。
func ComputePositionState(actions []Action, today time.Time) PositionState {
	sorted := make([]Action, len(actions))
	copy(sorted, actions)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].TradeDate.Before(sorted[j].TradeDate)
	})

	var lots []Lot
	var (
		episodeBuyCents  domain.Money // 当前段累计买入额（分）
		episodeSellCents domain.Money // 当前段累计卖出额（分，仅计已匹配部分）
		episodeRealized  domain.Money // 当前段累计已实现
		realizedPnL      domain.Money // 全局累计已实现
		realizedCarry    domain.Money // 已清仓段累计已实现
	)

	for _, a := range sorted {
		switch a.Type {
		case ActionBuy, ActionAdd:
			lots = append(lots, Lot{Price: a.Price, Shares: a.Shares, TradeDate: a.TradeDate})
			episodeBuyCents += a.Price.MulInt(a.Shares)

		case ActionSell:
			remaining := a.Shares
			for remaining > 0 && len(lots) > 0 {
				lot := &lots[0]
				take := remaining
				if take > lot.Shares {
					take = lot.Shares
				}
				// 该批实现的盈亏（分）= (卖出价 - 批次买入价) × 股数
				realized := (a.Price - lot.Price).MulInt(take)
				episodeRealized += realized
				realizedPnL += realized
				lot.Shares -= take
				remaining -= take
				if lot.Shares == 0 {
					lots = lots[1:] // 批次耗尽，出队
				}
			}
			// 卖出额只记“真正匹配到批次”的那部分，避免超卖时把空气也算进成本
			matched := a.Shares - remaining
			episodeSellCents += a.Price.MulInt(matched)

			// 清仓：本段结束，已实现计入 carry，本段状态重置
			if totalShares(lots) == 0 {
				realizedCarry += episodeRealized
				episodeBuyCents = 0
				episodeSellCents = 0
				episodeRealized = 0
			}
		}
	}

	total := totalShares(lots)
	var costPrice, fifoCostPrice domain.Money
	var weightedDays int
	if total > 0 {
		// 综合成本法：本轮净投入 ÷ 当前股数
		costPrice = (episodeBuyCents - episodeSellCents) / domain.Money(total)

		// FIFO 成本法：剩余批次按原始买入价加权
		var fifoNum domain.Money
		for _, l := range lots {
			fifoNum += l.Price.MulInt(l.Shares)
		}
		fifoCostPrice = fifoNum / domain.Money(total)

		// 按股数加权的平均持有天数
		var daySum int64
		for _, l := range lots {
			days := int64(math.Round(today.Sub(l.TradeDate).Hours() / 24.0))
			daySum += days * l.Shares
		}
		weightedDays = int(daySum / total)
	}

	return PositionState{
		Shares:        total,
		CostPrice:     costPrice,
		FIFOCostPrice: fifoCostPrice,
		WeightedDays:  weightedDays,
		RealizedPnL:   realizedPnL,
		RealizedCarry: realizedCarry,
		Lots:          lots,
	}
}

func totalShares(lots []Lot) int64 {
	var s int64
	for _, l := range lots {
		s += l.Shares
	}
	return s
}
