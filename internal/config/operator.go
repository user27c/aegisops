package config

import "fmt"

// OperatorConfig 是 aegisops-operator 的配置。
type OperatorConfig struct {
	Common CommonConfig
	// WatchNamespaces 是控制器监视的目标命名空间；为空表示全部。
	WatchNamespaces []string
	// DiagnosisURL 是诊断服务地址。
	DiagnosisURL string
	// DiagnosisTokenFile 是诊断服务 Token 文件路径（Secret 挂载）。
	DiagnosisTokenFile string
	// PrometheusURL / LokiURL / TempoURL 是证据采集端点。
	PrometheusURL string
	LokiURL       string
	TempoURL      string
	// ScaleMaxThrottledRatio 是 ScaleDeployment 验证允许的最大 CPU 限流比例。
	ScaleMaxThrottledRatio float64
	// LeaderElect 是否启用 leader election。
	LeaderElect bool
}

// LoadOperator 从环境变量加载 Operator 配置。
func LoadOperator(env Env) (OperatorConfig, error) {
	common, err := loadCommon(env)
	if err != nil {
		return OperatorConfig{}, err
	}
	leaderElect, err := getBool(env, "LEADER_ELECT", true)
	if err != nil {
		return OperatorConfig{}, err
	}
	scaleMaxThrottledRatio, err := getFloat(env, "SCALE_MAX_THROTTLED_RATIO", 0.10)
	if err != nil {
		return OperatorConfig{}, err
	}
	c := OperatorConfig{
		Common:                 common,
		WatchNamespaces:        SplitCSV(getString(env, "WATCH_NAMESPACES", "")),
		DiagnosisURL:           getString(env, "DIAGNOSIS_URL", ""),
		DiagnosisTokenFile:     getString(env, "DIAGNOSIS_TOKEN_FILE", ""),
		PrometheusURL:          getString(env, "PROMETHEUS_URL", ""),
		LokiURL:                getString(env, "LOKI_URL", ""),
		TempoURL:               getString(env, "TEMPO_URL", ""),
		ScaleMaxThrottledRatio: scaleMaxThrottledRatio,
		LeaderElect:            leaderElect,
	}
	if err := c.Validate(); err != nil {
		return OperatorConfig{}, err
	}
	return c, nil
}

// Validate 校验配置合法性。
func (c OperatorConfig) Validate() error {
	if err := requireNonEmpty(map[string]string{"CLUSTER_ID": c.Common.ClusterID}); err != nil {
		return err
	}
	if err := validateHTTPURL("DIAGNOSIS_URL", c.DiagnosisURL); err != nil {
		return err
	}
	if err := validateHTTPURL("PROMETHEUS_URL", c.PrometheusURL); err != nil {
		return err
	}
	if err := validateHTTPURL("LOKI_URL", c.LokiURL); err != nil {
		return err
	}
	if err := validateHTTPURL("TEMPO_URL", c.TempoURL); err != nil {
		return err
	}
	if c.DiagnosisURL != "" && c.DiagnosisTokenFile == "" {
		return fmt.Errorf("DIAGNOSIS_URL 已配置时必须同时配置 DIAGNOSIS_TOKEN_FILE")
	}
	if c.ScaleMaxThrottledRatio < 0 || c.ScaleMaxThrottledRatio > 1 {
		return fmt.Errorf("SCALE_MAX_THROTTLED_RATIO 必须在 [0,1]")
	}
	return nil
}
