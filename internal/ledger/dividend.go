package ledger

import (
	"sort"
	"time"

	"github.com/jwcen/argent-go/internal/domain"
)

// 分红 / 除权的纯计算部分。
//
// 这里要分清两条互不重叠的路径，混在一起就会双计：
//
//	路径 A「手工流水」——用户自己记一笔 DIVIDEND 动作。
//	        → 进 PositionState.IncomeRealized，算作已实现收益，不动成本价。
//
//	路径 B「除权事件」——来自交易所/行情源的客观分红送转事件（DividendEvent）。
//	        → 走 DiluteState 摊薄成本价，不进已实现收益。
//
// 同一笔钱只能走一条路径：要么把它当"落袋的收益"，要么把它当"降低了持仓成本"。
// 两边都记，浮动盈亏和已实现盈亏会同时变大，总收益凭空翻倍。
// 服务层通过 DividendEvent.Source 区分来源，并对手工流水做去重（见 portfolio 域）。

// DividendEvent 一次除权除息事件。
type DividendEvent struct {
	ExDate time.Time // 除权除息日
	// CashPerShare 每股派息（分，含税）。原始数据是"每 10 股派 X 元"，落库前已除以 10。
	CashPerShare domain.Money
	// BonusRatio 每股送转率。原始数据是"每 10 股送转 Y 股"，落库前已除以 10。
	// 例：10 送 3 转 2 → 0.5
	BonusRatio float64
}

// CumulativeCashDivPerShare 计算「开仓之后、今天之前」累计吃到的每股派息（分）。
//
// 边界条件是这个函数的全部难点，两头都要卡死：
//   - ex_date > since：除权日必须晚于开仓日。买在除权日当天或之后，
//     买到的已经是除权后的价格，这笔分红跟你没关系。
//   - ex_date <= today：还没到除权日的预案不能提前算。
//
// since 为零值时视为"不限开仓日"（把所有已发生的分红都算上）。
func CumulativeCashDivPerShare(events []DividendEvent, since, today time.Time) domain.Money {
	var total domain.Money
	for _, e := range events {
		if e.CashPerShare <= 0 {
			continue
		}
		if !since.IsZero() && !e.ExDate.After(since) {
			continue // ex_date <= since，买在除权后，吃不到
		}
		if e.ExDate.After(today) {
			continue // 还没除权
		}
		total += e.CashPerShare
	}
	return total
}

// DiluteState 用累计每股派息摊薄成本价，原地修改 st。
//
// 幂等性：始终以 CostPriceRaw / FIFOCostPriceRaw 为基准重算，
// 所以对同一个 state 调用两次、三次，结果都一样，不会重复扣减。
// （Python 原版靠"优先读已存在的 cost_price_raw"实现同样的保护。）
//
// 成本被摊到 0 以下时截断为 0：成本价是负数在业务上没有意义，
// 而且会让收益率分母翻转、前端显示成天文数字。
func DiluteState(st *PositionState, divPerShare domain.Money) {
	if st == nil {
		return
	}
	if divPerShare < 0 {
		divPerShare = 0
	}
	st.DividendPerShare = divPerShare
	st.CostPrice = clampZero(st.CostPriceRaw - divPerShare)
	st.FIFOCostPrice = clampZero(st.FIFOCostPriceRaw - divPerShare)
}

func clampZero(m domain.Money) domain.Money {
	if m < 0 {
		return 0
	}
	return m
}

// RestoreFromQFQ 把「前复权价」还原成「当时的真实成交价」（后复权方向）。
//
// 为什么需要它：所有主流行情源给的日 K 都是前复权（东财 fqt=1 / 腾讯 qfq），
// 前复权会把历史价格按后续每一次分红送转往下调，于是"三年前买入价 12.80"
// 在今天的 K 线上可能显示成 9.30。拿它跟用户记录的真实成本对比，就会错得离谱。
//
// 正向公式（raw → qfq）：qfq = (raw - 每股派息) / (1 + 每股送转率)
// 逆变换（qfq → raw）：  raw = qfq × (1 + 送转率) + 派息
//
// 必须**从最近的事件往最早回溯**地套用逆变换，顺序反了结果就错。
// 只处理 date < ex_date <= today 区间内的事件；配股不处理（原版同样不处理）。
func RestoreFromQFQ(qfqPrice domain.Money, events []DividendEvent, date, today time.Time) domain.Money {
	hits := make([]DividendEvent, 0, len(events))
	for _, e := range events {
		if e.ExDate.After(date) && !e.ExDate.After(today) {
			hits = append(hits, e)
		}
	}
	if len(hits) == 0 {
		return qfqPrice
	}
	// 由近及远
	sort.Slice(hits, func(i, j int) bool { return hits[i].ExDate.After(hits[j].ExDate) })

	px := qfqPrice.YuanF()
	for _, e := range hits {
		px = px*(1+e.BonusRatio) + e.CashPerShare.YuanF()
	}
	return domain.Yuan(px)
}
