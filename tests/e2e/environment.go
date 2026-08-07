// Package e2e 运行真实 Kind 集群上的端到端测试。
// 保护:必须显式设置 AEGISOPS_E2E=1 且 AEGISOPS_E2E_CONTEXT 指向 kind-aegisops-e2e,
// 由 scripts/e2e-up.sh 安装环境后调用 scripts/run-e2e.sh 运行。
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// Environment 汇总 e2e-up.sh 写入 .local/e2e/environment.json 的拓扑信息。
type Environment struct {
	Context         string `json:"context"`
	Namespace       string `json:"namespace"`       // run namespace(operator watch 的目标)
	SystemNamespace string `json:"systemNamespace"` // AegisOps 组件所在 namespace
	GatewayURL      string `json:"gatewayUrl"`
	IncidentAPIURL  string `json:"incidentApiUrl"`
	DiagnosisURL    string `json:"diagnosisUrl"`
	FaultLabURL     string `json:"faultLabUrl"`
	PrometheusURL   string `json:"prometheusUrl"`
	MailHogURL      string `json:"mailhogUrl"`
	WebhookToken    string `json:"webhookToken"`
	ApproverToken   string `json:"approverToken"`
	ViewerToken     string `json:"viewerToken"`
	Registry        string `json:"registry"`
	Tag             string `json:"tag"`

	K8s client.Client
}

var env *Environment

func init() {
	_ = opsv1alpha1.AddToScheme(scheme.Scheme)
}

// testEnv 返回 TestMain 加载的共享环境;未加载则测试失败。
func testEnv(t *testing.T) *Environment {
	t.Helper()
	if env == nil {
		t.Fatal("E2E 环境未加载(TestMain 失败或缺少 AEGISOPS_E2E=1)")
	}
	return env
}

// LoadEnvironment 读取 .local/e2e/environment.json 并建立 K8s 客户端。
func LoadEnvironment() (*Environment, error) {
	root := repoRoot()
	environmentPath := filepath.Join(root, ".local", "e2e", "environment.json")
	// #nosec G304 -- the path is a fixed repo-local E2E state file.
	data, err := os.ReadFile(environmentPath)
	if err != nil {
		return nil, fmt.Errorf("读取 environment.json: %w(先运行 scripts/e2e-up.sh)", err)
	}
	var e Environment
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("解析 environment.json: %w", err)
	}
	if e.Context == "" || e.Namespace == "" || e.GatewayURL == "" {
		return nil, fmt.Errorf("environment.json 字段缺失(context/namespace/gatewayUrl)")
	}
	if err := e.connect(); err != nil {
		return nil, err
	}
	return &e, nil
}

func (e *Environment) connect() error {
	cfg, err := config.GetConfigWithContext(e.Context)
	if err != nil {
		return fmt.Errorf("获取 context %q 配置: %w", e.Context, err)
	}
	if err := pingCluster(cfg); err != nil {
		return fmt.Errorf("context %q 不可达: %w", e.Context, err)
	}
	k8s, err := client.New(cfg, client.Options{})
	if err != nil {
		return fmt.Errorf("创建 K8s 客户端: %w", err)
	}
	e.K8s = k8s
	return nil
}

func repoRoot() string {
	wd, _ := os.Getwd()
	if strings.HasSuffix(wd, "/tests/e2e") {
		return strings.TrimSuffix(wd, "/tests/e2e")
	}
	return wd
}
