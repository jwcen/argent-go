package auth

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/smtp"
	"os"
	"strconv"
	"strings"
)

// SMTPMailer 通过 SMTP 把验证码真实发到用户邮箱（生产环境）。
// 配置来自环境变量 ARGENT_SMTP_*；未配置或开发环境下应继续使用 LogMailer。
type SMTPMailer struct {
	host   string
	port   int
	user   string
	pass   string
	from   string
	useSSL bool
	log    *slog.Logger
}

// SMTPConfig 描述 SMTP 发信配置。
type SMTPConfig struct {
	Host string
	Port int
	User string
	Pass string
	From string
	SSL  bool
}

// SMTPConfigFromEnv 从环境变量读取 SMTP 配置，并给默认（QQ 邮箱）。
func SMTPConfigFromEnv() SMTPConfig {
	host := os.Getenv("ARGENT_SMTP_HOST")
	if host == "" {
		host = "smtp.qq.com"
	}
	port := 465
	if v := os.Getenv("ARGENT_SMTP_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}
	ssl := true
	if v := os.Getenv("ARGENT_SMTP_SSL"); v != "" {
		ssl, _ = strconv.ParseBool(v)
	}
	user := os.Getenv("ARGENT_SMTP_USER")
	from := os.Getenv("ARGENT_SMTP_FROM")
	if from == "" {
		from = user
	}
	return SMTPConfig{
		Host: host,
		Port: port,
		User: user,
		Pass: os.Getenv("ARGENT_SMTP_PASS"),
		From: from,
		SSL:  ssl,
	}
}

// Enabled 表示 SMTP 已配置（host/user/pass 齐全）。
func (c SMTPConfig) Enabled() bool {
	return c.Host != "" && c.User != "" && c.Pass != ""
}

// NewSMTPMailer 构造 SMTPMailer。
func NewSMTPMailer(cfg SMTPConfig, log *slog.Logger) *SMTPMailer {
	if log == nil {
		log = slog.Default()
	}
	return &SMTPMailer{
		host:   cfg.Host,
		port:   cfg.Port,
		user:   cfg.User,
		pass:   cfg.Pass,
		from:   cfg.From,
		useSSL: cfg.SSL,
		log:    log,
	}
}

// SendCode 实现 Mailer：发送验证码邮件。
func (m *SMTPMailer) SendCode(ctx context.Context, email, code string) error {
	subject := "Argent 邮箱验证码"
	body := fmt.Sprintf(
		"您的 Argent 验证码是：%s\n\n该验证码 5 分钟内有效，请勿泄露给他人。\n如非本人操作，请忽略本邮件。",
		code,
	)
	if err := m.send(email, subject, body); err != nil {
		m.log.ErrorContext(ctx, "send verification email failed",
			slog.String("email", email), slog.Any("err", err))
		return err
	}
	m.log.InfoContext(ctx, "verification email sent", slog.String("email", email))
	return nil
}

// send 建立 SMTP 连接并投递邮件，支持 SSL(465) 与 STARTTLS(587) 两种模式。
func (m *SMTPMailer) send(to, subject, body string) error {
	from := m.from
	if from == "" {
		from = m.user
	}
	msg := buildMail(from, to, subject, body)
	addr := fmt.Sprintf("%s:%d", m.host, m.port)

	var c *smtp.Client
	var err error
	if m.useSSL {
		conn, derr := tls.Dial("tcp", addr, &tls.Config{ServerName: m.host})
		if derr != nil {
			return fmt.Errorf("tls dial: %w", derr)
		}
		defer conn.Close()
		c, err = smtp.NewClient(conn, m.host)
	} else {
		c, err = smtp.Dial(addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer c.Quit()

	if !m.useSSL {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if terr := c.StartTLS(&tls.Config{ServerName: m.host}); terr != nil {
				return fmt.Errorf("starttls: %w", terr)
			}
		}
	}

	auth := smtp.PlainAuth("", m.user, m.pass, m.host)
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("rcpt to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close data: %w", err)
	}
	return nil
}

// buildMail 组装 RFC 5322 邮件，主题与正文均按 UTF-8 编码，兼容中文。
func buildMail(from, to, subject, body string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: =?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?=\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("\r\n")
	b.WriteString(base64.StdEncoding.EncodeToString([]byte(body)))
	return b.String()
}
