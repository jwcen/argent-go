package transport

import (
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jwcen/argent-go/internal/auth"
)

// SessionCookieName 是会话 cookie 名，与 Python 原版及前端 useApi.js 保持一致。
const SessionCookieName = "argent_session"

// AuthHandler 把 auth.Service 适配成 HTTP 接口。
// 它只做三件事：解析请求 → 调用业务方法 → 把领域错误映射成 HTTP 状态码。
// 任何业务规则都不应该出现在这里。
type AuthHandler struct {
	svc *auth.Service
}

// NewAuthHandler 构造 handler。
func NewAuthHandler(svc *auth.Service) *AuthHandler { return &AuthHandler{svc: svc} }

// Register 挂载 /api/auth 下的免登录路由（项目铁律：路由归属写在 handler 自己这里）。
func (h *AuthHandler) Register(r gin.IRouter) {
	g := r.Group("/api/auth")
	g.POST("/send-code", h.SendCode)
	g.POST("/register", h.RegisterUser)
	g.POST("/login", h.Login)
	g.POST("/logout", h.Logout)
	g.GET("/me", h.Me)
}

type sendCodeReq struct {
	Email string `json:"email" binding:"required"`
}

type registerReq struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
	Code     string `json:"code" binding:"required"`
}

type loginReq struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// userDTO 是返回给前端的用户视图：绝不包含 password_hash。
// 领域模型与传输模型分离，避免「给结构体加个字段就意外泄露到 API」。
type userDTO struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

func toUserDTO(u *auth.User) userDTO {
	return userDTO{ID: u.ID, Email: u.Email}
}

// SendCode POST /api/auth/send-code
func (h *AuthHandler) SendCode(c *gin.Context) {
	var req sendCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "email is required")
		return
	}
	if err := h.svc.SendCode(c.Request.Context(), req.Email); err != nil {
		writeAuthError(c, err)
		return
	}
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

// RegisterUser POST /api/auth/register —— 注册成功即登录，直接下发 cookie。
func (h *AuthHandler) RegisterUser(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "email, password and code are required")
		return
	}
	user, token, err := h.svc.Register(c.Request.Context(), req.Email, req.Password, req.Code)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	h.setSessionCookie(c, token)
	WriteJSON(c, http.StatusOK, toUserDTO(user))
}

// Login POST /api/auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "email and password are required")
		return
	}
	user, token, err := h.svc.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	h.setSessionCookie(c, token)
	WriteJSON(c, http.StatusOK, toUserDTO(user))
}

// Logout POST /api/auth/logout —— 幂等：没登录也返回 200。
func (h *AuthHandler) Logout(c *gin.Context) {
	if token, err := c.Cookie(SessionCookieName); err == nil {
		if err := h.svc.Logout(c.Request.Context(), token); err != nil {
			writeAuthError(c, err)
			return
		}
	}
	h.clearSessionCookie(c)
	WriteJSON(c, http.StatusOK, gin.H{"ok": true})
}

// Me GET /api/auth/me —— 返回当前登录用户，未登录 401。
func (h *AuthHandler) Me(c *gin.Context) {
	token, err := c.Cookie(SessionCookieName)
	if err != nil {
		WriteError(c, http.StatusUnauthorized, "not authenticated")
		return
	}
	user, err := h.svc.Authenticate(c.Request.Context(), token)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	WriteJSON(c, http.StatusOK, toUserDTO(user))
}

// setSessionCookie 下发会话 cookie。
//
// HttpOnly：JS 读不到，抵御 XSS 窃取会话。
// SameSite=Lax：跨站请求不带 cookie，抵御大部分 CSRF。
// Secure：仅生产开启，本地 http 调试时若开启浏览器会拒绝保存。
func (h *AuthHandler) setSessionCookie(c *gin.Context, token string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(
		SessionCookieName,
		token,
		int(h.svc.SessionTTL().Seconds()),
		"/",
		"",               // domain 留空 = 当前主机
		h.cookieSecure(c), // Secure：仅当连接或前置代理为 HTTPS
		true,             // HttpOnly
	)
}

func (h *AuthHandler) clearSessionCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookieName, "", -1, "/", "", h.cookieSecure(c), true)
}

// cookieSecure 决定会话 cookie 是否带 Secure 标志。
//
// 规则：仅当「当前连接是 HTTPS」或「前置代理通过 X-Forwarded-Proto 声明是 HTTPS」时才标记 Secure。
// 这样在尚未配置 HTTPS 的直接 HTTP 访问下（如开发机 / 裸 ECS IP）浏览器仍能保存 cookie，
// 登录后才能正常携带；一旦套上 Nginx/HTTPS 反代，自动升级为 Secure。
// 环境变量 ARGENT_COOKIE_SECURE 可强制覆盖（true/false），用于特殊部署或调试。
func (h *AuthHandler) cookieSecure(c *gin.Context) bool {
	if v := os.Getenv("ARGENT_COOKIE_SECURE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	if c.Request.TLS != nil {
		return true
	}
	if c.GetHeader("X-Forwarded-Proto") == "https" {
		return true
	}
	return false
}

// writeAuthError 把领域哨兵错误映射为 HTTP 状态码。
//
// 这个函数是「业务层不认识 HTTP」这条边界的具体体现：
// auth 包只返回 ErrXxx，翻译成 401/409/429 是 transport 层的职责。
func writeAuthError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		WriteError(c, http.StatusUnauthorized, "邮箱或密码错误")
	case errors.Is(err, auth.ErrUnauthenticated):
		WriteError(c, http.StatusUnauthorized, "not authenticated")
	case errors.Is(err, auth.ErrEmailTaken):
		WriteError(c, http.StatusConflict, "该邮箱已注册")
	case errors.Is(err, auth.ErrInvalidEmail):
		WriteError(c, http.StatusBadRequest, "邮箱格式不正确")
	case errors.Is(err, auth.ErrWeakPassword):
		WriteError(c, http.StatusBadRequest, "密码至少 8 位")
	case errors.Is(err, auth.ErrCodeCooldown):
		WriteError(c, http.StatusTooManyRequests, "验证码发送过于频繁，请稍后再试")
	case errors.Is(err, auth.ErrCodeInvalid):
		WriteError(c, http.StatusBadRequest, "验证码无效或已过期")
	case errors.Is(err, auth.ErrCodeAttemptsExceeded):
		WriteError(c, http.StatusTooManyRequests, "验证码尝试次数过多，请重新获取")
	default:
		// 未预期错误：不把内部细节暴露给客户端，详情由 Recovery/日志中间件记录。
		WriteError(c, http.StatusInternalServerError, "internal server error")
	}
}
