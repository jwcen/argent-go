package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// defTool 把一个 Go 函数封装成 eino 可调用的工具。
//
//	info    —— 工具的元信息（名称/描述/参数 JSON Schema），直接决定 LLM 何时调用它；
//	handler —— 业务实现，入参是已反序列化的 args 结构体，返回 (结果字符串, error)。
//
// 结果字符串会作为 tool_result 回灌给模型；过长会在调用方（ReAct 循环）里截断，
// 避免单个工具把上下文撑爆。
func defTool[T any, D any](
	info *schema.ToolInfo,
	handler func(ctx context.Context, args T) (string, error),
) (tool.InvokableTool, error) {
	return utils.NewTool[T, D](info, func(ctx context.Context, args T) (D, error) {
		res, err := handler(ctx, args)
		var zero D
		if err != nil {
			// 工具执行失败：回一个失败说明字符串，让模型知道原因并继续推理，
			// 而不是向上抛错导致 eino tools 节点整轮失败（一个工具挂了不该拖垮整个回答）。
			// 这条规则对所有工具统一生效，对齐老版"优雅降级"的行为。
			out := "工具执行失败：" + err.Error()
			return any(truncateToolResult(out)).(D), nil
		}
		// D 这里统一是 string：工具直接返回已格式化文本。
		out, ok := any(res).(string)
		if !ok {
			// 兜底：若 handler 返回了结构化 D，序列化回字符串。
			b, mErr := json.Marshal(res)
			if mErr != nil {
				return zero, mErr
			}
			out = string(b)
		}
		return any(truncateToolResult(out)).(D), nil
	}), nil
}

// maxToolResultLen 单个工具返回的最大长度，超出截断（保护 LLM 上下文）。
const maxToolResultLen = 6000

func truncateToolResult(s string) string {
	if len(s) <= maxToolResultLen {
		return s
	}
	return s[:maxToolResultLen] + fmt.Sprintf("\n…(结果已截断，原长 %d 字符)", len(s))
}

// strParam 构造一个 string 类型参数定义的便捷函数。
func strParam(desc string, required bool) *schema.ParameterInfo {
	p := &schema.ParameterInfo{Type: schema.String, Desc: desc}
	if required {
		p.Required = true
	}
	return p
}

func intParam(desc string, required bool) *schema.ParameterInfo {
	p := &schema.ParameterInfo{Type: schema.Integer, Desc: desc}
	if required {
		p.Required = true
	}
	return p
}

func arrStrParam(desc string, required bool) *schema.ParameterInfo {
	p := &schema.ParameterInfo{Type: schema.Array, Desc: desc, ElemInfo: &schema.ParameterInfo{Type: schema.String}}
	if required {
		p.Required = true
	}
	return p
}

// objectSchema 构造一个只有 properties 的 object 参数 schema。
func objectSchema(props map[string]*schema.ParameterInfo) *schema.ParameterInfo {
	return &schema.ParameterInfo{
		Type:     schema.Object,
		SubParams: props,
	}
}

// AllTools 返回当前已实现并接入 agent 的全部工具。
// 新增工具：在对应 tools_*.go 里实现 + 在这里登记即可，ReAct 循环自动可用。
func (s *Service) AllTools(ctx context.Context) ([]tool.BaseTool, error) {
	var (
		built []tool.BaseTool
		err   error
	)
	add := func(t tool.InvokableTool, e error) {
		if e != nil {
			err = e
			return
		}
		built = append(built, t)
	}

	// ── 个股行情类 ──
	add(s.toolGetQuote())
	add(s.toolGetTrend())
	add(s.toolGetChainQuote())
	add(s.toolGetFundQuote())
	add(s.toolResolveStock())

	// ── 用户数据类 ──
	add(s.toolGetHoldings())
	add(s.toolGetAssetAllocation())
	add(s.toolGetTrades())
	add(s.toolGetThesis())

	if err != nil {
		return nil, err
	}
	return built, nil
}
