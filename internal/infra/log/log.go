// Package log 提供基于标准库 log/slog 的结构化日志工厂。
//
// 设计目标（对标生产项目）：
//   - 结构化输出（json 用于生产，text 用于本地），字段可被 Loki / ELK / Datadog 直接解析
//   - 日志级别、格式、输出目标全部由环境变量注入（12-factor）
//   - 通过 context 自动为每条日志带上 request_id 等请求级字段（见 handler.go 的 ctxHandler）
//   - 不依赖任何三方日志库，仅用一个 slog.Handler 包装器实现 context 注入
//
// 本包属于“基础设施层”，但只依赖标准库，因此业务域、transport 层都能无副作用地复用同一条 logger。
package log

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config 控制 logger 的行为。所有字段为空时由 New 按 ARGENT_ENV 给出默认值。
type Config struct {
	Level  string // debug | info | warn | error，空则由环境决定
	Format string // json | text，空则由环境决定
	Output string // stdout | stderr | 文件路径，空则 stdout
}

// ParseLevel 把字符串级别映射到 slog.Level，未知值回落到 info。
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New 构建并返回一个 *slog.Logger。
// 环境语义：ARGENT_ENV=production 时默认 json + info；否则默认 text + debug。
// 各字段可被 Config 显式覆盖（例如运维想临时调成 debug）。
// 返回的 logger 已被 ctxHandler 包装，能自动注入 context 中的请求级字段。
func New(cfg Config) *slog.Logger {
	env := strings.ToLower(os.Getenv("ARGENT_ENV"))

	level := cfg.Level
	if level == "" {
		if env == "production" {
			level = "info"
		} else {
			level = "debug"
		}
	}
	format := cfg.Format
	if format == "" {
		if env == "production" {
			format = "json"
		} else {
			format = "text"
		}
	}

	w, err := resolveOutput(cfg.Output)
	if err != nil {
		// 输出目标打不开时回退 stdout，并直接写 stderr 告警（避免“鸡生蛋”：不能再用 logger 去记 logger 的错误）
		fmt.Fprintf(os.Stderr, "log: cannot open output %q: %v; falling back to stdout\n", cfg.Output, err)
		w = os.Stdout
	}

	return NewToWriter(w, level, format)
}

// NewToWriter 把日志写到指定的 io.Writer，级别/格式显式给定。
// 主要用于测试与需要自定义输出的场景（例如把日志接进内存 buffer 或第三方 writer）。
// 与 New 一样，返回的 logger 也经过 ctxHandler 包装。
func NewToWriter(w io.Writer, level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:     ParseLevel(level),
		AddSource: strings.EqualFold(level, "debug"), // 仅 debug 级带源码位置，避免生产性能损耗
	}
	return slog.New(&ctxHandler{handler: baseHandler(w, format, opts)})
}

// baseHandler 根据格式选择 slog 的内置 Handler（JSON 或 Text）。
func baseHandler(w io.Writer, format string, opts *slog.HandlerOptions) slog.Handler {
	switch strings.ToLower(format) {
	case "text", "console":
		return slog.NewTextHandler(w, opts)
	case "json", "":
		return slog.NewJSONHandler(w, opts)
	default:
		return slog.NewJSONHandler(w, opts)
	}
}

// resolveOutput 根据配置返回 io.Writer；空值回落到 stdout。
func resolveOutput(out string) (io.Writer, error) {
	switch strings.ToLower(out) {
	case "", "stdout":
		return os.Stdout, nil
	case "stderr":
		return os.Stderr, nil
	default:
		return os.OpenFile(out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	}
}
