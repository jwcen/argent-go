package auth

import "context"

// ctxKey 是本包私有的 context key 类型。
//
// 用未导出的自定义类型（而不是 string）做 key 是 Go 的强制惯例：
// context 的键空间是全局共享的，如果用 string("user")，别的包用同名字符串
// 就会静默覆盖你的值。私有类型让键在类型层面唯一，外部无法伪造。
type ctxKey struct{ name string }

var userKey = ctxKey{"auth.user"}

// ContextWithUser 把已认证用户放入 context，由中间件调用。
func ContextWithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// UserFromContext 取出当前用户；未认证时返回 nil, false。
func UserFromContext(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(userKey).(*User)
	return u, ok && u != nil
}

// MustUserID 取当前用户 ID，未认证时返回 0。
// 供已过 RequireAuth 中间件的 handler 使用。
func MustUserID(ctx context.Context) int64 {
	if u, ok := UserFromContext(ctx); ok {
		return u.ID
	}
	return 0
}
