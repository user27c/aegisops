package config

import "fmt"

// AuthMode 是 Incident API 的认证模式。
type AuthMode string

const (
	// AuthModeDisabled 仅允许本地开发，生产配置必须拒绝。
	AuthModeDisabled AuthMode = "disabled"
	// AuthModeStaticTokens 使用静态 Token 文件认证。
	AuthModeStaticTokens AuthMode = "static-tokens"
)

// APIConfig 是 incident-api 的配置。
type APIConfig struct {
	Common CommonConfig
	// ListenAddr 是监听地址。
	ListenAddr string
	// AuthMode 是认证模式。
	AuthMode AuthMode
	// StaticTokensFile 是 Token 文件路径（AUTH_MODE=static-tokens 时必填）。
	StaticTokensFile string
	// WebDistDir 是 Web 静态文件目录。
	WebDistDir string
	// AllowedOrigins 是允许的 CORS 来源。
	AllowedOrigins []string
	// WatchNamespaces 是控制台 API 被授权读取的命名空间。必须与 Helm
	// 为 incident-api 创建 Role 的命名空间保持一致，禁止退化为集群级 List。
	WatchNamespaces []string
	// DiagnosisURL 与 DiagnosisTokenFile 用于代理证据/时间线接口。
	DiagnosisURL       string
	DiagnosisTokenFile string
}

// LoadAPI 从环境变量加载 API 配置。
func LoadAPI(env Env) (APIConfig, error) {
	common, err := loadCommon(env)
	if err != nil {
		return APIConfig{}, err
	}
	authMode := AuthMode(getString(env, "AUTH_MODE", string(AuthModeDisabled)))
	c := APIConfig{
		Common:             common,
		ListenAddr:         getString(env, "LISTEN_ADDR", ":8080"),
		AuthMode:           authMode,
		StaticTokensFile:   getString(env, "STATIC_TOKENS_FILE", ""),
		WebDistDir:         getString(env, "WEB_DIST_DIR", ""),
		AllowedOrigins:     SplitCSV(getString(env, "ALLOWED_ORIGINS", "")),
		WatchNamespaces:    SplitCSV(getString(env, "WATCH_NAMESPACES", "")),
		DiagnosisURL:       getString(env, "DIAGNOSIS_URL", ""),
		DiagnosisTokenFile: getString(env, "DIAGNOSIS_TOKEN_FILE", ""),
	}
	if err := c.Validate(); err != nil {
		return APIConfig{}, err
	}
	return c, nil
}

// Validate 校验配置合法性。
func (c APIConfig) Validate() error {
	if err := requireNonEmpty(map[string]string{"CLUSTER_ID": c.Common.ClusterID}); err != nil {
		return err
	}
	if len(c.WatchNamespaces) == 0 {
		return fmt.Errorf("配置项 WATCH_NAMESPACES 不能为空，incident-api 不允许集群级读取")
	}
	switch c.AuthMode {
	case AuthModeDisabled:
		// 本地开发允许；生产部署由 Helm values 强制 static-tokens。
	case AuthModeStaticTokens:
		if err := requireNonEmpty(map[string]string{"STATIC_TOKENS_FILE": c.StaticTokensFile}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("AUTH_MODE 取值 %q 不合法，允许 disabled/static-tokens", c.AuthMode)
	}
	if err := validateHTTPURL("DIAGNOSIS_URL", c.DiagnosisURL); err != nil {
		return err
	}
	if c.DiagnosisURL != "" && c.DiagnosisTokenFile == "" {
		return fmt.Errorf("DIAGNOSIS_URL 已配置时必须同时配置 DIAGNOSIS_TOKEN_FILE")
	}
	return nil
}
