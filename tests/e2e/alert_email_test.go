package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestE2EAlertEmail 场景 E:AegisOpsTargetDown → Alertmanager → MailHog firing/resolved 邮件。
//
// 验证:
//  1. 将 faultlab ServiceMonitor 指向不存在的 metrics path → target down → critical 告警
//  2. MailHog 收到 FIRING 邮件
//  3. 恢复副本 → RESOLVED 邮件
//  4. 邮件正文不含 .local/secrets 中的测试 token(Secret 不泄漏)
func TestE2EAlertEmail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	e := testEnv(t)

	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := RestoreFaultLab(c, e); err != nil {
			t.Logf("恢复 faultlab 失败: %v", err)
		}
		_ = e.K8s.DeleteAllOf(c, &opsv1alpha1.AIOpsIncident{}, client.InNamespace(e.Namespace))
	})

	secretValues := []string{}
	for _, f := range []string{"webhook-token", "console-tokens", "diagnosis-token"} {
		secretPath := filepath.Join(repoRoot(), ".local", "secrets", f)
		// #nosec G304 -- f comes from the fixed allowlist above and is only used in E2E tests.
		if raw, err := os.ReadFile(secretPath); err == nil {
			secretValues = append(secretValues, strings.Fields(string(raw))...)
		}
	}
	if err := ClearMailHog(ctx, e); err != nil {
		t.Fatal(err)
	}

	// 保留 endpoint，只让 Prometheus 抓取失败，确保 up=0 而非 target 消失。
	if err := SetFaultLabMetricsPath(ctx, e, "/e2e-missing-metrics"); err != nil {
		t.Fatal(err)
	}

	mailTimeout := 8 * time.Minute
	if err := AssertAlertEmailReceived(ctx, e, "FIRING", mailTimeout); err != nil {
		t.Fatalf("未收到 FIRING 邮件: %v", err)
	}
	t.Log("收到 FIRING 邮件")

	// 恢复抓取路径。
	if err := SetFaultLabMetricsPath(ctx, e, "/metrics"); err != nil {
		t.Fatal(err)
	}
	if err := AssertAlertEmailReceived(ctx, e, "RESOLVED", mailTimeout); err != nil {
		t.Fatalf("未收到 RESOLVED 邮件: %v", err)
	}
	t.Log("收到 RESOLVED 邮件")

	// Secret 不泄漏:全量邮件正文不应包含任何本地 secret token。
	raw := dumpAllMailhogBodies(ctx, e)
	for _, tok := range secretValues {
		if len(tok) >= 8 && strings.Contains(raw, tok) {
			t.Fatal("邮件正文检测到 secret 泄漏")
		}
	}
	t.Log("邮件正文无 secret 泄漏")
}

func dumpAllMailhogBodies(ctx context.Context, e *Environment) string {
	var sb strings.Builder
	if resp, err := httpGet(ctx, e.MailHogURL+"/api/v2/messages"); err == nil {
		sb.WriteString(resp)
	}
	return sb.String()
}
