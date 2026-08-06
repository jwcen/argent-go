package portfolio

// A 股交易手续费估算。
//
// 四项组成（与原版 position_ledger.estimate_trade_fee 对齐）：
//   1. 佣金 commission = max(amount * rate, min)   买卖双向
//   2. 印花税 stamp = amount * 0.0005               仅卖出（2023-08 起）
//   3. 过户费 transfer = amount * 0.00001           买卖双向（沪深都收）
//   4. 规费 regulatory = amount * (经手费 + 证管费)  买卖双向
//
// 默认费率用招商证券万1.854 / 最低5元，与原版一致。
// 传入 broker 的费率则用 broker 的值。

const (
	defaultCommissionRate = 0.0001854 // 万1.854
	defaultCommissionMin  = 5.0       // ¥5
	stampRate             = 0.0005    // 0.05% 卖出
	transferRate          = 0.00001   // 万0.1 双向
	exchangeHandleRate    = 0.0000341 // 经手费 万0.341
	regulatoryFeeRate     = 0.00002   // 证管费 万0.2
)

// EstimateTradeFee 估算一笔 A 股交易的总手续费（元）。
//
// actionType: "BUY"/"ADD" → 买入侧；"SELL" → 卖出侧（加印花税）；其他 → 0
// broker 为 nil 时用默认费率。
func EstimateTradeFee(actionType ActionType, price float64, shares int64, b *Broker) float64 {
	if b != nil {
		return estimateFee(actionType, price, shares, b.StockRate, b.StockMin)
	}
	return estimateFee(actionType, price, shares, defaultCommissionRate, defaultCommissionMin)
}

func estimateFee(t ActionType, price float64, shares int64, commissionRate, commissionMin float64) float64 {
	// 非买入/卖出（如 ADD 红股 price=0）不收费
	isBuy := t == ActionBuy
	isSell := t == ActionSell
	if !isBuy && !isSell {
		return 0
	}

	amount := price * float64(shares)
	if amount <= 0 {
		return 0
	}

	commission := amount * commissionRate
	if commission < commissionMin {
		commission = commissionMin
	}

	var stamp float64
	if isSell {
		stamp = amount * stampRate
	}

	transfer := amount * transferRate
	regulatory := amount * (exchangeHandleRate + regulatoryFeeRate)

	return commission + stamp + transfer + regulatory
}
