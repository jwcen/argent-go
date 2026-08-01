package auth

import (
	"context"
	"log/slog"
)

// LogMailer 是开发环境的 Mailer 实现：把验证码写进日志而非真的发信。
//
// 这样本地开发/自测无需配置 SMTP —— 从 logs/argent.log 里就能拿到验证码。
// 生产环境务必替换成真实的 SMTP 实现，否则验证码会明文落在日志里。
type LogMailer struct {
	Logger *slog.Logger
}

// NewLogMailer 构造一个 LogMailer。
func NewLogMailer(logger *slog.Logger) *LogMailer {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogMailer{Logger: logger}
}

// SendCode 实现 Mailer。
func (m *LogMailer) SendCode(ctx context.Context, email, code string) error {
	m.Logger.WarnContext(ctx, "DEV MAILER: verification code not actually sent",
		slog.String("email", email),
		slog.String("code", code),
	)
	return nil
}
