package auth

import "errors"

// 哨兵错误（sentinel errors）。
//
// Go 里表达「可预期的失败分支」的惯例做法：定义包级 error 变量，调用方用
// errors.Is 判断。相比自定义 error 类型更轻，相比字符串比较更安全。
// transport 层据此把领域错误映射成 HTTP 状态码，业务层则完全不认识 HTTP。
var (
	// ErrNotFound 由 Repository 在查不到记录时返回。
	ErrNotFound = errors.New("auth: not found")

	// ErrEmailTaken 邮箱已注册。
	ErrEmailTaken = errors.New("auth: email already registered")

	// ErrInvalidCredentials 邮箱或密码错误。
	// 刻意不区分「用户不存在」和「密码错误」，避免账号枚举攻击。
	ErrInvalidCredentials = errors.New("auth: invalid email or password")

	// ErrInvalidEmail 邮箱格式非法。
	ErrInvalidEmail = errors.New("auth: invalid email address")

	// ErrWeakPassword 密码不满足最低强度要求。
	ErrWeakPassword = errors.New("auth: password too short")

	// ErrCodeCooldown 距上次发码不足冷却时间。
	ErrCodeCooldown = errors.New("auth: verification code requested too frequently")

	// ErrCodeInvalid 验证码不存在、不匹配或已过期。
	ErrCodeInvalid = errors.New("auth: invalid or expired verification code")

	// ErrCodeAttemptsExceeded 验证码尝试次数超限，需重新获取。
	ErrCodeAttemptsExceeded = errors.New("auth: too many verification attempts")

	// ErrUnauthenticated 无有效会话。
	ErrUnauthenticated = errors.New("auth: not authenticated")
)
