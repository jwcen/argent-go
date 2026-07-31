package transport

import "github.com/gin-gonic/gin"

// New 构建 gin 引擎并挂载所有 transport 层路由。
// 目前只有 health；后续 Stage 会在 bootstrap 里把各域 handler 传进来、在这里注册。
func New() *gin.Engine {
	r := gin.Default() // 自带 Logger + Recovery 中间件

	hh := &HealthHandler{}
	hh.Register(r)

	return r
}
