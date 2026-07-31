package main

import (
	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/transport"
)

// Build 组装整个应用：config → store → services → handlers → router → server。
// 现在是手写依赖注入（DI）的唯一入口：所有依赖在这里显式 new 出来、串起来，
// 避免包级全局变量满天飞。后续 Stage 往里逐步注入 store / service / handler。
func Build() *gin.Engine {
	return transport.New()
}
