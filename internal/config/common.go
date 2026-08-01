// Package config 负责从环境变量加载并校验各组件配置。
//
// 约定：Secret 只能通过 *_FILE 方式读取；Validate 必须拒绝生产模式下的不安全配置。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Env 提供环境变量读取的轻量封装，便于测试替换。
type Env interface {
	Getenv(key string) string
}

// OSEnv 是默认的环境变量实现。
type OSEnv struct{}

// Getenv 读取环境变量。
func (OSEnv) Getenv(key string) string { return os.Getenv(key) }

// getString 读取字符串环境变量；为空时返回 defaultValue。
func getString(env Env, key, defaultValue string) string {
	if v := env.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// getBool 读取布尔环境变量；解析失败返回错误而不是静默降级。
func getBool(env Env, key string, defaultValue bool) (bool, error) {
	v := env.Getenv(key)
	if v == "" {
		return defaultValue, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("环境变量 %s 的值 %q 不是合法布尔值: %w", key, v, err)
	}
	return b, nil
}

// getInt 读取整数环境变量。
func getInt(env Env, key string, defaultValue int64) (int64, error) {
	v := env.Getenv(key)
	if v == "" {
		return defaultValue, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("环境变量 %s 的值 %q 不是合法整数: %w", key, v, err)
	}
	return n, nil
}

// SplitCSV 把逗号分隔的列表拆为去空格后的切片；空输入返回 nil。
func SplitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// CommonConfig 是所有组件共享的配置。
type CommonConfig struct {
	// LogLevel 是日志级别（debug/info/warn/error）。
	LogLevel string
	// ClusterID 是集群逻辑 ID，用于指纹与审计。
	ClusterID string
	// OTelEndpoint 是 OTLP 导出端点；为空时不导出 Trace。
	OTelEndpoint string
}

// loadCommon 从环境变量加载公共配置。
func loadCommon(env Env) (CommonConfig, error) {
	c := CommonConfig{
		LogLevel:     getString(env, "LOG_LEVEL", "info"),
		ClusterID:    getString(env, "CLUSTER_ID", ""),
		OTelEndpoint: getString(env, "OTEL_EXPORTER_OTLP_ENDPOINT", ""),
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return c, fmt.Errorf("LOG_LEVEL 取值 %q 不合法，允许 debug/info/warn/error", c.LogLevel)
	}
	return c, nil
}

// requireNonEmpty 校验必填字段。
func requireNonEmpty(fields map[string]string) error {
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("配置项 %s 不能为空", name)
		}
	}
	return nil
}

// validateHTTPURL 校验 URL 必须是 http/https。
func validateHTTPURL(name, value string) error {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("配置项 %s 不是合法 URL: %w", name, err)
	}
	switch u.Scheme {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("配置项 %s 只允许 http/https，当前为 %q", name, u.Scheme)
	}
}
