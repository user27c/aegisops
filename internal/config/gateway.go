package config

import "fmt"

// GatewayConfig 是 alert-gateway 的配置。
type GatewayConfig struct {
	Common CommonConfig
	// ListenAddr 是监听地址。
	ListenAddr string
	// WebhookBearerTokenFile 是 Alertmanager Webhook 共享 Token 文件路径（必填）。
	WebhookBearerTokenFile string
	// MaxBodyBytes 是 Webhook 请求体上限（字节）。
	MaxBodyBytes int64
	// MetricsAddr 是指标端点监听地址。
	MetricsAddr string
}

// LoadGateway 从环境变量加载 Gateway 配置。
func LoadGateway(env Env) (GatewayConfig, error) {
	common, err := loadCommon(env)
	if err != nil {
		return GatewayConfig{}, err
	}
	maxBodyBytes, err := getInt(env, "MAX_BODY_BYTES", 1<<20)
	if err != nil {
		return GatewayConfig{}, err
	}
	c := GatewayConfig{
		Common:                 common,
		ListenAddr:             getString(env, "LISTEN_ADDR", ":8080"),
		WebhookBearerTokenFile: getString(env, "WEBHOOK_BEARER_TOKEN_FILE", ""),
		MaxBodyBytes:           maxBodyBytes,
		MetricsAddr:            getString(env, "METRICS_ADDR", ":8080"),
	}
	if err := c.Validate(); err != nil {
		return GatewayConfig{}, err
	}
	return c, nil
}

// Validate 校验配置合法性。
func (c GatewayConfig) Validate() error {
	if err := requireNonEmpty(map[string]string{
		"CLUSTER_ID":                c.Common.ClusterID,
		"WEBHOOK_BEARER_TOKEN_FILE": c.WebhookBearerTokenFile,
	}); err != nil {
		return err
	}
	if c.MaxBodyBytes < 1024 || c.MaxBodyBytes > 4<<20 {
		return fmt.Errorf("MAX_BODY_BYTES 必须在 1024 到 4MiB 之间，当前为 %d", c.MaxBodyBytes)
	}
	return nil
}
