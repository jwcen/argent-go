package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// filteringModel 是 model.ToolCallingChatModel 的装饰器：
// 在模型输出交给 react 图的 toolsNode 之前，过滤掉 name 为空或不在
// 本次已注册工具集合内的 tool_call。某些 OpenAI 兼容模型偶发吐出
// name="" 的畸形 tool_call，会令 eino 的 toolsNode 直接抛
// "tool not found in toolsNode indexes" 并拖垮整轮；本装饰器把这类
// 畸形调用静默丢弃，保留合法调用，使单点故障不影响整轮 ReAct。
//
// 允许的工具名集合在 WithTools 时捕获——react 在装配图时会调用
// WithTools(tools) 绑定工具，这正是本回合实际使用的工具清单。
type filteringModel struct {
	inner   model.ToolCallingChatModel
	allowed map[string]bool
}

// wrapWithToolFilter 包一层过滤装饰器（allowed 在 WithTools 时填充）。
func wrapWithToolFilter(inner model.ToolCallingChatModel) model.ToolCallingChatModel {
	return &filteringModel{inner: inner}
}

func (w *filteringModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner2, err := w.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(tools))
	for _, t := range tools {
		allowed[t.Name] = true
	}
	return &filteringModel{inner: inner2, allowed: allowed}, nil
}

func (w *filteringModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	msg, err := w.inner.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	if msg != nil {
		filterMessageToolCalls(msg, w.allowed)
	}
	return msg, nil
}

func (w *filteringModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, err := w.inner.Stream(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	// 把整条流读完，合并成单条完整消息（tool_call 分片需按 index 汇聚），
	// 再过滤畸形 tool_call，最后用单元素 reader 回灌给 react 图。
	var chunks []*schema.Message
	for {
		m, rerr := sr.Recv()
		if rerr != nil {
			break
		}
		if m != nil {
			chunks = append(chunks, m)
		}
	}
	merged := mergeMessageChunks(chunks)
	if merged != nil {
		filterMessageToolCalls(merged, w.allowed)
	}
	return schema.StreamReaderFromArray([]*schema.Message{merged}), nil
}

// filterMessageToolCalls 原地丢弃 name 为空或不在 allowed 集合内的 tool_call。
func filterMessageToolCalls(msg *schema.Message, allowed map[string]bool) {
	if msg == nil || len(msg.ToolCalls) == 0 {
		return
	}
	kept := make([]schema.ToolCall, 0, len(msg.ToolCalls))
	dropped := 0
	for _, tc := range msg.ToolCalls {
		name := tc.Function.Name
		if name == "" || !allowed[name] {
			dropped++
			continue
		}
		kept = append(kept, tc)
	}
	msg.ToolCalls = kept
	_ = dropped
}

// mergeMessageChunks 把流式分片聚合成一条完整 assistant 消息。
// tool_call 分片按 Index 汇聚（复刻 eino schema.concatToolCalls 的语义）。
func mergeMessageChunks(chunks []*schema.Message) *schema.Message {
	if len(chunks) == 0 {
		return nil
	}
	out := &schema.Message{Role: schema.Assistant}
	var sb strings.Builder
	flat := make([]schema.ToolCall, 0, len(chunks))
	for _, c := range chunks {
		if c == nil {
			continue
		}
		if c.Role != "" {
			out.Role = c.Role
		}
		if c.Content != "" {
			sb.WriteString(c.Content)
		}
		if len(c.ToolCalls) > 0 {
			flat = append(flat, c.ToolCalls...)
		}
		if c.ResponseMeta != nil {
			out.ResponseMeta = c.ResponseMeta
		}
	}
	out.Content = sb.String()
	out.ToolCalls = concatToolCalls(flat)
	return out
}

// concatToolCalls 按 Index 汇聚分片（复刻 eino 内部逻辑，仅本包可见）。
func concatToolCalls(chunks []schema.ToolCall) []schema.ToolCall {
	if len(chunks) == 0 {
		return nil
	}
	m := make(map[int][]int)
	for i := range chunks {
		index := chunks[i].Index
		if index == nil {
			// 无 index：视为独立完整调用，原样保留。
			continue
		}
		m[*index] = append(m[*index], i)
	}

	merged := make([]schema.ToolCall, 0, len(chunks))
	// 先放无 index 的独立调用
	for i := range chunks {
		if chunks[i].Index == nil {
			merged = append(merged, chunks[i])
		}
	}

	for k, v := range m {
		index := k
		toolCall := schema.ToolCall{Index: &index}
		if len(v) > 0 {
			toolCall = chunks[v[0]]
		}
		var args strings.Builder
		toolID, toolType, toolName := "", "", ""
		for _, n := range v {
			chunk := chunks[n]
			if chunk.ID != "" {
				if toolID == "" {
					toolID = chunk.ID
				} else if toolID != chunk.ID {
					// 不同 id 的分片不应归入同一 index，直接合并首个。
					_ = fmt.Errorf("tool id mismatch in stream merge: %s %s", toolID, chunk.ID)
				}
			}
			if chunk.Type != "" {
				if toolType == "" {
					toolType = chunk.Type
				}
			}
			if chunk.Function.Name != "" {
				if toolName == "" {
					toolName = chunk.Function.Name
				}
			}
			if chunk.Function.Arguments != "" {
				args.WriteString(chunk.Function.Arguments)
			}
		}
		toolCall.ID = toolID
		toolCall.Type = toolType
		toolCall.Function.Name = toolName
		toolCall.Function.Arguments = args.String()
		merged = append(merged, toolCall)
	}

	if len(merged) > 1 {
		sort.SliceStable(merged, func(i, j int) bool {
			iVal, jVal := merged[i].Index, merged[j].Index
			if iVal == nil && jVal == nil {
				return false
			} else if iVal == nil && jVal != nil {
				return true
			} else if iVal != nil && jVal == nil {
				return false
			}
			return *iVal < *jVal
		})
	}
	return merged
}
