package transport

import (
	"time"

	"github.com/gin-gonic/gin"
)

// HealthHandler 暴露进程健康探针。
// Stage 0 还没有业务逻辑，所以直接放在 transport 层（框架适配器）；
// 等 Stage 2+ 各业务域 handler 出现后，再按域拆分出去。
type HealthHandler struct{}

// Health 返回 200 + 简单状态，供探活（curl / k8s liveness）。
func (h *HealthHandler) Health(c *gin.Context) {
	WriteJSON(c, 200, gin.H{
		"status": "ok",
		"ts":     time.Now().UnixMilli(),
	})
}

// Register 把本 handler 的路由挂到 /api 分组下。
// 这是项目铁律（D4）：路由归属写在 handler 自己的 Register 里，
// 由 router.go 统一调用，从根本上杜绝巨石路由文件。
func (h *HealthHandler) Register(r gin.IRouter) {
	g := r.Group("/api")
	g.GET("/health", h.Health)
}
