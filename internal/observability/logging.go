// Package observability 提供日志、Trace 与 Prometheus 指标的初始化与公共类型。
package observability

import (
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger 创建结构化 JSON 日志器。级别为 debug/info/warn/error。
func NewLogger(level string) (logr.Logger, error) {
	var zapLevel zapcore.Level
	switch level {
	case "debug":
		zapLevel = zapcore.DebugLevel
	case "info":
		zapLevel = zapcore.InfoLevel
	case "warn":
		zapLevel = zapcore.WarnLevel
	case "error":
		zapLevel = zapcore.ErrorLevel
	default:
		return logr.Logger{}, fmt.Errorf("未知日志级别 %q", level)
	}

	cfg := zap.Config{
		Level:       zap.NewAtomicLevelAt(zapLevel),
		Development: false,
		Encoding:    "json",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}
	z, err := cfg.Build()
	if err != nil {
		return logr.Logger{}, fmt.Errorf("初始化 zap logger: %w", err)
	}
	return zapr.NewLogger(z), nil
}

// MustLogger 在初始化失败时直接退出进程，仅用于 main 启动阶段。
func MustLogger(level string) logr.Logger {
	logger, err := NewLogger(level)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	return logger
}
