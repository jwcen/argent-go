package agent

import (
	"context"
	"io"
	"sync"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// reactAgentKey 按模型名缓存编译好的 react 图（避免每请求重编译）。
var (
	reactCacheMu sync.Mutex
	reactCache   = map[string]*react.Agent{}
)

// buildReactAgent 构造（并缓存）一个带全部工具的 ReAct agent。
// 工具事件是"按请求"通过 ctx 里的 ToolEventSink 透出的，所以 agent 本身可安全缓存。
func (s *Service) buildReactAgent(ctx context.Context, modelName string) (*react.Agent, error) {
	reactCacheMu.Lock()
	if a, ok := reactCache[modelName]; ok {
		reactCacheMu.Unlock()
		return a, nil
	}
	reactCacheMu.Unlock()

	tools, err := s.AllTools(ctx)
	if err != nil {
		return nil, err
	}

	// 工具中间件：在工具真正执行前后把事件发到 ctx 里的 sink。
	mw := compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				emitTool(ctx, ToolEvent{Name: input.Name, Args: input.Arguments, Phase: "call"})
				out, rerr := next(ctx, input)
				if rerr != nil {
					emitTool(ctx, ToolEvent{Name: input.Name, Error: rerr.Error(), Phase: "error"})
					return nil, rerr
				}
				emitTool(ctx, ToolEvent{Name: input.Name, Result: out.Result, Phase: "result"})
				return out, nil
			}
		},
	}

	// 复用 Chat 的模型构造逻辑，但包成 ToolCallingChatModel。
	cm, err := s.buildToolCallingModel(ctx, modelName)
	if err != nil {
		return nil, err
	}

	cfg := &react.AgentConfig{
		ToolCallingModel: cm,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: tools, ToolCallMiddlewares: []compose.ToolMiddleware{mw}},
		MessageModifier:  react.NewPersonaModifier(systemPromptForAgent()),
		MaxStep:          12,
	}

	agent, err := react.NewAgent(ctx, cfg)
	if err != nil {
		return nil, err
	}

	reactCacheMu.Lock()
	reactCache[modelName] = agent
	reactCacheMu.Unlock()
	return agent, nil
}

// ChatStreamReAct 跑带工具的 ReAct 问答，把工具事件经 ctx sink 透给前端，
// 文本答案以 channel 流式返回。
func (s *Service) ChatStreamReAct(ctx context.Context, messages []*schema.Message) (<-chan string, error) {
	modelName := s.orderedCandidates()[0]
	agent, err := s.buildReactAgent(ctx, modelName)
	if err != nil {
		return nil, err
	}
	stream, err := agent.Stream(ctx, messages)
	if err != nil {
		return nil, err
	}
	out := make(chan string, 64)
	go func() {
		defer close(out)
		for {
			msg, rerr := stream.Recv()
			if rerr == io.EOF {
				return
			}
			if rerr != nil {
				return
			}
			if msg != nil && msg.Content != "" {
				out <- msg.Content
			}
		}
	}()
	return out, nil
}

// systemPromptForAgent 给 agent 的系统提示：强调工具使用纪律与输出规范。
func systemPromptForAgent() string {
	return "你是市场&个股解读 + 理财规划助手。回答关于个股涨跌/消息面/持仓关系、" +
		"市场风格/资金主线、资产配置/现金理财的问题。\n" +
		"【名称→代码一律实解析】涉及任何标的，先用 resolve_stock 把名字/行业词解析成标准代码，" +
		"再传给 get_quote/get_trend 等工具；不要凭记忆编造代码。\n" +
		"【每个数值挂标的名】每个具体数值(价位/涨跌幅/成本/量比)都紧跟所属标的名。\n" +
		"【个股问题】先 resolve_stock 取代码，再 get_quote+get_trend；分析涨跌把量价作为主轴。" +
		"涉及用户自身数据(持仓/交易/配置/买入逻辑)优先用 get_holdings/get_trades/get_asset_allocation/get_thesis。\n" +
		"【客观克制】只基于工具返回的真实数据下结论，不编造；风险提示要明确但不过度恐慌。\n" +
		"用中文作答，必要时用分点/小标题让结构清晰。"
}
