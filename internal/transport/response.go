package transport

import "github.com/gin-gonic/gin"

// errBody 对齐前端 useApi.js 期望的错误格式：{"detail": ...}
type errBody struct {
	Detail string `json:"detail"`
}

// WriteJSON 以 JSON 编码返回任意值。
// 用 gin 的 c.JSON 即可——它会自动按正确顺序设 Content-Type、写状态码、编码 body。
func WriteJSON(c *gin.Context, code int, v any) {
	c.JSON(code, v)
}

// WriteError 返回统一错误体 {"detail": ...}，前端依赖这个结构。
func WriteError(c *gin.Context, code int, detail string) {
	c.JSON(code, errBody{Detail: detail})
}
