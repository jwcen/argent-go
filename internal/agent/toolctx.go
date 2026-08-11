package agent

import (
	"context"
	"database/sql"
	"errors"
)

// ToolEvent 是 agent 一次工具调用的可见事件，用于前端把"调了哪些工具"展示出来。
// Phase: "call"（开始调用，带参数）/ "result"（返回，带结果）/ "error"（执行失败）。
type ToolEvent struct {
	Name   string `json:"name"`
	Args   string `json:"args,omitempty"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
	Phase  string `json:"phase"`
}

// ToolEventSink 接收工具事件的回调。transport 层实现它，转发成 SSE 事件。
type ToolEventSink func(ToolEvent)

// ── 请求级上下文透传 ──
// agent 是单例（react 图只编译一次），但每个请求有自己的：
//   - 工具事件 sink（前端可视化）
//   - 当前用户身份（userID + 取库函数），供用户数据类工具取库
// 这两样都通过 ctx 在中间件/工具执行链里传递，避免每请求重编译图。

type toolSinkKey struct{}

// WithToolSink 把工具事件回调挂进 ctx。
func WithToolSink(ctx context.Context, sink ToolEventSink) context.Context {
	return context.WithValue(ctx, toolSinkKey{}, sink)
}

func toolSinkFromCtx(ctx context.Context) ToolEventSink {
	if v, ok := ctx.Value(toolSinkKey{}).(ToolEventSink); ok {
		return v
	}
	return nil
}

// emitTool 安全地把一次工具事件发出去（sink 为空时静默忽略）。
func emitTool(ctx context.Context, ev ToolEvent) {
	if s := toolSinkFromCtx(ctx); s != nil {
		s(ev)
	}
}

// ── 流错误透传 ──
// ReAct 图跑超 MaxStep 或内部报错时，stream.Recv() 返回 error 而非 io.EOF。
// ChatStreamReAct 通过 ErrorSink 把这类错误推给 transport 层，前端能看到具体原因，
// 而不是流静默截断、用户只看到一段不完整的答案。

type errorSinkKey struct{}

// ErrorSink 接收流级错误。
type ErrorSink func(error)

// WithErrorSink 把流错误回调挂进 ctx。
func WithErrorSink(ctx context.Context, sink ErrorSink) context.Context {
	return context.WithValue(ctx, errorSinkKey{}, sink)
}

func errorSinkFromCtx(ctx context.Context) ErrorSink {
	if v, ok := ctx.Value(errorSinkKey{}).(ErrorSink); ok {
		return v
	}
	return nil
}

// emitError 安全地把流错误发出去（sink 为空时静默忽略）。
func emitError(ctx context.Context, err error) {
	if s := errorSinkFromCtx(ctx); s != nil {
		s(err)
	}
}

type userCtxKey struct{}

type userScope struct {
	userID int64
	dbFn   func(userID int64) (*sql.DB, error)
}

// WithUserScope 把当前请求的用户身份挂进 ctx，供用户数据类工具取库后构造 service。
func WithUserScope(ctx context.Context, userID int64, dbFn func(userID int64) (*sql.DB, error)) context.Context {
	return context.WithValue(ctx, userCtxKey{}, &userScope{userID: userID, dbFn: dbFn})
}

func userScopeFromCtx(ctx context.Context) *userScope {
	if v, ok := ctx.Value(userCtxKey{}).(*userScope); ok {
		return v
	}
	return nil
}

// errNoUser 表示请求上下文里没有用户身份（用户数据类工具无法工作）。
var errNoUser = errors.New("agent: 当前请求缺少用户身份")

// UserScopeFromCtx 暴露当前请求的用户取库能力，供工具内部构造用户级 service。
// 返回 userID、取库函数、是否齐备。
func UserScopeFromCtx(ctx context.Context) (int64, func(userID int64) (*sql.DB, error), bool) {
	us := userScopeFromCtx(ctx)
	if us == nil {
		return 0, nil, false
	}
	return us.userID, us.dbFn, true
}

// ── 用户级 service 工厂（由 transport 注入，agent 包不直接依赖 sqlite）──
// 这些接口只描述工具所需的读能力，transport 注入真实 *portfolio.Service / *external.Service。

// PortfolioStore 是持仓类工具对 portfolio service 的最小需求（返回 agent 领域结构）。
type PortfolioStore interface {
	ListHoldings(ctx context.Context) ([]PortfolioHolding, error)
	ListActions(ctx context.Context, code string) ([]PortfolioAction, error)
	GetThesis(ctx context.Context, code string) (*PortfolioThesis, error)
}

// ExternalStore 是场外资产类工具对 external service 的最小需求（返回 agent 领域结构）。
type ExternalStore interface {
	ListAssets(ctx context.Context) ([]ExternalAsset, error)
}

type userSvcKey struct{}

type userServices struct {
	portfolio PortfolioStore
	external  ExternalStore
}

// WithUserServices 把已构造好的用户级 service 注入 ctx。
// p 必须实现 PortfolioStore，e 必须实现 ExternalStore。transport 层用适配器包装真实 service。
func WithUserServices(ctx context.Context, p PortfolioStore, e ExternalStore) context.Context {
	return context.WithValue(ctx, userSvcKey{}, &userServices{portfolio: p, external: e})
}

func userServicesFromCtx(ctx context.Context) *userServices {
	if v, ok := ctx.Value(userSvcKey{}).(*userServices); ok {
		return v
	}
	return nil
}

// ensure interfaces are satisfied at compile time by the adapter type built in transport.
var (
	_ PortfolioStore = (PortfolioStore)(nil)
	_ ExternalStore  = (ExternalStore)(nil)
)

// ── 工具所需的领域结构（从真实 service 返回值拷贝而来，agent 包不依赖其构造器）──

// PortfolioHolding 持仓一行。
type PortfolioHolding struct {
	StockCode    string  `json:"stock_code"`
	StockName    string  `json:"stock_name"`
	Shares       int64   `json:"shares"`
	CostPrice    float64 `json:"cost_price"`
	WeightedDays int     `json:"weighted_days"`
}

// PortfolioAction 成交流水一行。
type PortfolioAction struct {
	StockCode  string  `json:"stock_code"`
	ActionType string  `json:"action_type"`
	Price      float64 `json:"price"`
	Shares     int64   `json:"shares"`
	TradeDate  string  `json:"trade_date"`
	Note       string  `json:"note"`
}

// PortfolioThesis 买入逻辑。
type PortfolioThesis struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Thesis string `json:"thesis"`
}

// ExternalAsset 场外资产一行。
type ExternalAsset struct {
	AssetType string   `json:"asset_type"`
	Name      string   `json:"name"`
	CostAmount float64 `json:"cost_amount"`
	Shares    *float64 `json:"shares,omitempty"`
}
