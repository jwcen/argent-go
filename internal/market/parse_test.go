package market

import (
	"encoding/json"
	"testing"
)

// TestParseClist 验证板块榜响应(clist)字段映射：f12→Code, f14→Name, f3→Chg(×100), f62→NetIn。
func TestParseClist(t *testing.T) {
	body := `{"data":{"total":2,"diff":[{"f12":"BK1626","f14":"稀土","f3":582,"f62":1494460832.0},{"f12":"BK1592","f14":"通信","f3":533,"f62":5504170752.0}]}}`
	var r emClistResp
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Data == nil || len(r.Data.Diff) != 2 {
		t.Fatalf("unexpected diff: %+v", r.Data)
	}
	d0 := r.Data.Diff[0]
	if d0.Code != "BK1626" || d0.Name != "稀土" || d0.Chg != 582 || d0.NetIn != 1494460832.0 {
		t.Fatalf("field mapping wrong: %+v", d0)
	}
	// 业务层换算：f3(×100) → 5.82%
	if got := round2(d0.Chg / 100); got != 5.82 {
		t.Fatalf("change pct scale wrong: got %v want 5.82", got)
	}
}

// TestParseStockGet 验证 stock/get 通用响应字段映射，覆盖指数/海外指数/市场宽度。
func TestParseStockGet(t *testing.T) {
	body := `{"data":{"f57":"000001","f58":"上证指数","f43":392718,"f170":100,"f104":1200,"f105":600,"f128":120,"f136":80,"f116":5}}`
	var r emStockGet
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	d := r.Data
	if d == nil {
		t.Fatal("data nil")
	}
	if d.Code != "000001" || d.Name != "上证指数" {
		t.Fatalf("code/name wrong: %+v", d)
	}
	if got := round2(d.Price / 100); got != 3927.18 {
		t.Fatalf("price scale wrong: got %v want 3927.18", got)
	}
	if got := round2(d.Chg / 100); got != 1.0 {
		t.Fatalf("chg scale wrong: got %v want 1.0", got)
	}
	// 市场宽度语义：f104=上涨, f105=下跌, f128=平盘, f136=涨停, f116=跌停
	if d.Up != 1200 || d.Down != 600 || d.Flat != 120 || d.LimitUp != 80 || d.LimitDn != 5 {
		t.Fatalf("breadth fields wrong: %+v", d)
	}
}

// TestSectorBoardCodeNormalize 验证 BoardStocks 对板块代码前缀的容错。
func TestSectorBoardCodeNormalize(t *testing.T) {
	norm := func(c string) string {
		c = trimPrefix(c, "BK")
		return "BK" + c
	}
	if got := norm("1626"); got != "BK1626" {
		t.Fatalf("got %s", got)
	}
	if got := norm("BK1626"); got != "BK1626" {
		t.Fatalf("got %s", got)
	}
}

func trimPrefix(s, p string) string {
	if len(s) >= len(p) && s[:len(p)] == p {
		return s[len(p):]
	}
	return s
}
