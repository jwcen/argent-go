package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// stubModel 仅实现我们要测的三方法，其余靠嵌入的接口零值（不会被调用）。
type stubModel struct {
	model.ToolCallingChatModel
	out *schema.Message
}

func (s *stubModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return s.out, nil
}

func (s *stubModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{s.out}), nil
}

func (s *stubModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return s, nil
}

func TestFilteringModelDropsMalformedToolCalls(t *testing.T) {
	idx0, idx1, idx2 := 0, 1, 2
	// 一条消息里混合：合法 get_quote、空名、未知 get_market_indices。
	msg := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{Index: &idx0, ID: "c1", Function: schema.FunctionCall{Name: "get_quote", Arguments: `{"code":"600519"}`}},
			{Index: &idx1, ID: "c2", Function: schema.FunctionCall{Name: "", Arguments: `{"x":1}`}},                  // 空名畸形
			{Index: &idx2, ID: "c3", Function: schema.FunctionCall{Name: "get_market_indices", Arguments: `{}`}},      // 未注册
		},
	}

	stub := &stubModel{out: msg}
	wrapped := wrapWithToolFilter(stub)

	tools := []*schema.ToolInfo{
		{Name: "get_quote"}, {Name: "get_holdings"},
		{Name: "get_trend"}, {Name: "get_chain_quote"},
		{Name: "get_fund_quote"}, {Name: "get_asset_allocation"},
		{Name: "get_trades"}, {Name: "get_thesis"}, {Name: "resolve_stock"},
	}
	w2, err := wrapped.WithTools(tools)
	if err != nil {
		t.Fatalf("WithTools err: %v", err)
	}

	// Generate 路径
	got, err := w2.Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("Generate err: %v", err)
	}
	if len(got.ToolCalls) != 1 {
		t.Fatalf("期望保留 1 个合法 tool_call, 实际 %d: %+v", len(got.ToolCalls), got.ToolCalls)
	}
	if got.ToolCalls[0].Function.Name != "get_quote" {
		t.Fatalf("期望保留 get_quote, 实际 %s", got.ToolCalls[0].Function.Name)
	}

	// Stream 路径：需要合并分片后再过滤，验证空名/未知被丢、合法保留。
	sr, err := w2.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	var merged *schema.Message
	for {
		m, rerr := sr.Recv()
		if rerr != nil {
			break
		}
		merged = m
	}
	if merged == nil || len(merged.ToolCalls) != 1 {
		t.Fatalf("Stream 期望合并后保留 1 个合法 tool_call, 实际 %d: %+v", len(merged.ToolCalls), merged)
	}
	if merged.ToolCalls[0].Function.Name != "get_quote" {
		t.Fatalf("Stream 期望 get_quote, 实际 %s", merged.ToolCalls[0].Function.Name)
	}
	if merged.ToolCalls[0].Function.Arguments != `{"code":"600519"}` {
		t.Fatalf("Stream 参数丢失: %s", merged.ToolCalls[0].Function.Arguments)
	}
}

func TestFilterMessageToolCallsEmptyInput(t *testing.T) {
	// 无 tool_call 的消息不应 panic。
	m := &schema.Message{Role: schema.Assistant, Content: "hi"}
	filterMessageToolCalls(m, map[string]bool{"get_quote": true})
	if m.Content != "hi" {
		t.Fatalf("内容被破坏: %s", m.Content)
	}
}
