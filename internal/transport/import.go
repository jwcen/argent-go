package transport

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/agent"
)

// ImportHandler 截图导入：把投资 App 截图识别成结构化记录草稿。
//
// 只做「识别」这一件事，不负责写入——用户确认草稿后，
// 前端仍走现有 /api/assets（基金）与 /api/portfolio（股票）接口创建。
// 职责单一：识别器不碰业务写入，避免双份创建逻辑。
type ImportHandler struct {
	agent  *agent.Service
	logger *slog.Logger
}

func NewImportHandler(a *agent.Service, logger *slog.Logger) *ImportHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ImportHandler{agent: a, logger: logger}
}

func (h *ImportHandler) Register(r gin.IRouter) {
	g := r.Group("/import")
	g.POST("/screenshot", h.Screenshot)
}

// ImportRecord 一条待用户确认的导入草稿。
type ImportRecord struct {
	Kind       string  `json:"kind"`                 // fund | stock
	Code       string  `json:"code"`                 // 6 位代码
	Name       string  `json:"name"`                 // 名称
	ActionType string  `json:"action_type"`          // BUY | ADD | REDEEM | SELL
	Amount     float64 `json:"amount,omitempty"`     // 基金：本金/赎回金额（元）
	Shares     float64 `json:"shares,omitempty"`     // 份额（小数）或股数
	Nav        float64 `json:"nav,omitempty"`        // 基金单位净值
	Price      float64 `json:"price,omitempty"`      // 股票单价
	TradeDate  string  `json:"trade_date,omitempty"` // YYYY-MM-DD
	Platform   string  `json:"platform,omitempty"`   // 支付宝/天天基金等
	Fee        float64 `json:"fee,omitempty"`        // 手续费
	Status     string  `json:"status,omitempty"`     // confirmed | pending（T+1 未确认）
}

type screenshotResp struct {
	Records []ImportRecord `json:"records"`
}

const screenshotSystemPrompt = "你是投资记账助手，负责把用户上传的 App 截图（基金/券商持仓页、交易记录页）解析成结构化数据。\n" +
	"只输出一个 JSON 对象，不要任何解释、不要 markdown 代码块标记（不要用三反引号 json 包住）。\n" +
	`JSON 结构：{"records":[{"kind":"fund|stock","code":"6位代码","name":"名称","action_type":"BUY|ADD|REDEEM|SELL","amount":1234.56,"shares":1234.56,"nav":1.234,"price":12.34,"trade_date":"YYYY-MM-DD","platform":"平台名","fee":0.0,"status":"confirmed|pending"}]}` + "\n" +
	"规则：\n" +
	"- kind：场外基金/ETF/理财=fund，A股股票=stock；无法判断时默认 fund。\n" +
	"- code：去掉空格和交易所前缀，只留 6 位数字；截图没有代码就留空字符串。\n" +
	"- action_type：买入=BUY，加仓/定投追加=ADD，赎回=REDEEM，卖出=SELL。\n" +
	"- amount：基金填本金或赎回金额（元）；股票可不填或填成交金额。\n" +
	"- shares：基金填份额（可小数）；股票填股数。\n" +
	"- nav：基金的单位净值；price：股票的单价。截图上能看到哪个填哪个。\n" +
	"- trade_date：截图上的交易/净值日期，转成 YYYY-MM-DD；看不到就留空字符串。\n" +
	"- status：截图标注「待确认/确认中」等未确认状态的填 pending，否则 confirmed。\n" +
	"- 专注解析截图里出现的投资记录内容，忽略界面上其他无关文字（如「识别失败」「0 条记录」、按钮文案、App 导航等）。只要截图里出现了基金/股票列表、持仓金额、收益等投资信息，就要把它们提取出来。\n" +
	"- 只有当截图中完全没有任何投资/持仓/交易相关信息（如纯首页、K 线图、新闻文章）时，才返回 {\"records\":[]}。\n" +
	"- 数字一律用数字类型，不要用字符串；空值填 0 或省略该字段。"

// POST /api/import/screenshot
//
// 支持两种提交方式：
//  1. multipart/form-data，字段名 file（图片文件）
//  2. JSON：{"image":"<base64>","mime":"image/png"}
func (h *ImportHandler) Screenshot(c *gin.Context) {
	if h.agent == nil || !h.agent.IsConfigured() {
		WriteError(c, http.StatusNotImplemented, "未配置 LLM，无法识别截图")
		return
	}

	file, err := c.FormFile("file")
	if err == nil {
		f, err := file.Open()
		if err != nil {
			WriteError(c, http.StatusInternalServerError, "读取图片失败")
			return
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			WriteError(c, http.StatusInternalServerError, "读取图片失败")
			return
		}
		mime := file.Header.Get("Content-Type")
		if mime == "" {
			mime = mimeByExt(file.Filename)
		}
		h.parseAndRespond(c, base64.StdEncoding.EncodeToString(data), mime)
		return
	}

	// 兼容 base64 JSON
	var body struct {
		Image string `json:"image"`
		Mime  string `json:"mime"`
	}
	if err := c.ShouldBindJSON(&body); err == nil && body.Image != "" {
		mime := body.Mime
		if mime == "" {
			mime = "image/png"
		}
		h.parseAndRespond(c, body.Image, mime)
		return
	}

	WriteError(c, http.StatusBadRequest, "需要上传图片（multipart file 或 base64 JSON）")
}

func (h *ImportHandler) parseAndRespond(c *gin.Context, imageB64, mime string) {
	text, err := h.agent.ParseImage(
		c.Request.Context(),
		imageB64,
		mime,
		screenshotSystemPrompt,
		"请解析这张截图里的投资记录，按规则输出 JSON。",
	)
	if err != nil {
		h.logger.Warn("import/screenshot: llm parse failed", "err", err)
		WriteError(c, http.StatusUnprocessableEntity, "识别失败："+err.Error())
		return
	}
	records, err := parseScreenshotJSON(text)
	if err != nil {
		h.logger.Warn("import/screenshot: llm output not json", "err", err, "raw", truncateStr(text, 400))
		WriteError(c, http.StatusUnprocessableEntity, "识别结果不是合法 JSON，请重试或手工录入")
		return
	}
	h.logger.Info("import/screenshot: parsed records", "count", len(records))
	WriteJSON(c, http.StatusOK, screenshotResp{Records: records})
}

// parseScreenshotJSON 从 LLM 回复中提取 records。
// 容错：允许被 ```json 代码块包裹，也允许直接是数组。
func parseScreenshotJSON(text string) ([]ImportRecord, error) {
	t := strings.TrimSpace(text)
	if i := strings.Index(t, "{"); i > 0 {
		t = t[i:]
	}
	if i := strings.LastIndex(t, "}"); i >= 0 {
		t = t[:i+1]
	}
	var obj screenshotResp
	if err := json.Unmarshal([]byte(t), &obj); err == nil {
		return obj.Records, nil
	}
	var arr []ImportRecord
	if err := json.Unmarshal([]byte(t), &arr); err == nil {
		return arr, nil
	}
	return nil, errJSON
}

var errJSON = &jsonError{}

type jsonError struct{}

func (e *jsonError) Error() string { return "invalid json" }

func mimeByExt(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".jpg"), strings.HasSuffix(n, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(n, ".png"):
		return "image/png"
	case strings.HasSuffix(n, ".webp"):
		return "image/webp"
	case strings.HasSuffix(n, ".heic"):
		return "image/heic"
	default:
		return "image/png"
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
