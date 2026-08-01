package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/auth"
)

// SessionCookieName 与 transport 包保持一致。
// 定义在这里而不是 import transport，是为了避免 transport ↔ middleware 循环依赖。
const SessionCookieName = "argent_session"

// Authenticator 是 RequireAuth 需要的最小能力集。
//
// 这是 Go 的经典风格：「接受接口，返回结构体」。中间件不依赖具体的
// *auth.Service，只依赖一个单方法接口，测试时塞个假实现就行。
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (*auth.User, error)
}

// RequireAuth 校验 session cookie，通过则把用户注入 context 并放行，
// 否则直接 401 中断——后续 handler 完全不必再写鉴权代码。
//
// 契约（见 wiki/02-API清单）：除 /api/auth/* 外所有接口都需要该 cookie。
func RequireAuth(a Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(SessionCookieName)
		if err != nil || token == "" {
			abortUnauthorized(c)
			return
		}

		user, err := a.Authenticate(c.Request.Context(), token)
		if err != nil || user == nil {
			abortUnauthorized(c)
			return
		}

		// 同时放进 gin 的 Keys 和标准 context：
		// 前者方便 handler 快速取用，后者让 service/repo 层也能拿到（无需传 gin.Context）。
		c.Set("user_id", user.ID)
		c.Request = c.Request.WithContext(auth.ContextWithUser(c.Request.Context(), user))

		c.Next()
	}
}

func abortUnauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"detail": "not authenticated"})
}
