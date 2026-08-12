package e2e

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestE2ERestoreConfigMapFromImmutableBackup proves the complete local chain:
// a mounted ConfigMap causes the container to enter CrashLoopBackOff, the
// operator proposes RestoreConfigMap, and approval restores the target from an
// immutable backup so the application becomes healthy again.
func TestE2ERestoreConfigMapFromImmutableBackup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	e := testEnv(t)
	const alertName = "ContainerCrashLooping"
	fp := "sha256:e2e-config-restore-0001"
	incName := IncidentName(e, alertName, fp)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cleanupCancel()
		if err := RestoreCheckoutConfig(cleanupCtx, e); err != nil {
			t.Logf("恢复 checkout-config 失败: %v", err)
		}
		_ = RestoreFaultLab(cleanupCtx, e)
		_ = e.K8s.DeleteAllOf(cleanupCtx, &opsv1alpha1.AIOpsIncident{}, client.InNamespace(e.Namespace))
	})

	backup, err := getConfigMap(ctx, e, "checkout-config-backup")
	if err != nil {
		t.Fatal(err)
	}
	if backup.Immutable == nil || !*backup.Immutable {
		t.Fatal("checkout-config-backup 必须 immutable")
	}
	if backup.Data["mode"] != "healthy" {
		t.Fatalf("immutable backup mode=%q, want healthy", backup.Data["mode"])
	}

	target, err := getConfigMap(ctx, e, "checkout-config")
	if err != nil {
		t.Fatal(err)
	}
	if target.Data["mode"] != "healthy" {
		t.Fatalf("前置 target mode=%q, want healthy", target.Data["mode"])
	}

	// The fault is a data change to the mounted ConfigMap, not a process
	// endpoint call and not a Deployment mutation.
	if err := SetCheckoutConfigMode(ctx, e, "crashloop"); err != nil {
		t.Fatal(err)
	}
	target, err = getConfigMap(ctx, e, "checkout-config")
	if err != nil {
		t.Fatal(err)
	}
	if target.Data["mode"] != "crashloop" {
		t.Fatalf("ConfigMap fault 未写入，mode=%q", target.Data["mode"])
	}
	if err := WaitForCrashLoopBackOff(ctx, e, 4*time.Minute); err != nil {
		_ = DumpDiagnostics(ctx, e, t.TempDir(), e.Namespace, incName)
		t.Fatal(err)
	}
	if CheckoutHealthy(ctx, e) {
		t.Fatal("ConfigMap crashloop 模式下 /checkout 不应健康")
	}

	resp, err := PostAlert(ctx, e, map[string]string{
		"alertname": alertName,
		"namespace": e.Namespace,
		"workload":  "faultlab",
		"severity":  "critical",
	}, fp, "firing")
	if err != nil {
		t.Fatalf("发送 CrashLoop 告警: %v", err)
	}
	if resp.Rejected > 0 || resp.Accepted < 1 {
		t.Fatalf("CrashLoop 告警未被接受: %+v", resp)
	}

	inc, err := WaitIncidentPhase(ctx, e, e.Namespace, incName, opsv1alpha1.PhaseAwaitingApproval, 4*time.Minute)
	if err != nil {
		_ = DumpDiagnostics(ctx, e, t.TempDir(), e.Namespace, incName)
		t.Fatal(err)
	}
	if inc.Status.Proposal == nil || inc.Status.Proposal.Action != opsv1alpha1.ActionRestoreConfigMap {
		t.Fatalf("预期 RestoreConfigMap，实际 %+v", inc.Status.Proposal)
	}
	var params map[string]any
	if err := json.Unmarshal(inc.Status.Proposal.Parameters.Raw, &params); err != nil {
		t.Fatalf("解析 RestoreConfigMap 参数: %v", err)
	}
	if params["targetConfigMap"] != "checkout-config" || params["backupConfigMap"] != "checkout-config-backup" {
		t.Fatalf("RestoreConfigMap 参数不正确: %+v", params)
	}

	if err := ApproveIncident(ctx, e, e.Namespace, incName, "e2e approve immutable ConfigMap restore"); err != nil {
		t.Fatal(err)
	}
	_, err = WaitIncidentPhase(ctx, e, e.Namespace, incName, opsv1alpha1.PhaseResolved, 6*time.Minute)
	if err != nil {
		_ = DumpDiagnostics(ctx, e, t.TempDir(), e.Namespace, incName)
		t.Fatal(err)
	}

	target, err = getConfigMap(ctx, e, "checkout-config")
	if err != nil {
		t.Fatal(err)
	}
	if target.Data["mode"] != backup.Data["mode"] {
		t.Fatalf("RestoreConfigMap 未恢复 target: target=%q backup=%q", target.Data["mode"], backup.Data["mode"])
	}
	backupAfter, err := getConfigMap(ctx, e, "checkout-config-backup")
	if err != nil {
		t.Fatal(err)
	}
	if backupAfter.Immutable == nil || !*backupAfter.Immutable || backupAfter.Data["mode"] != "healthy" {
		t.Fatalf("immutable backup 被修改: immutable=%v data=%v", backupAfter.Immutable, backupAfter.Data)
	}
	if target.Annotations["ops.aegis.io/operation-id"] == "" {
		t.Fatal("恢复后的 target 缺少 operation-id 审计注解")
	}
	if err := WaitFaultLabHealthy(ctx, e, 3*time.Minute); err != nil {
		t.Fatal(err)
	}

	tr, err := QueryAuditTimeline(ctx, e, e.Namespace, incName)
	if err != nil {
		t.Fatal(err)
	}
	assertTimelineTypes(t, tr, "ApprovalGranted", "ExecutionStarted", "ExecutionCompleted", "IncidentResolved")
	t.Log("ConfigMap → CrashLoopBackOff → immutable backup RestoreConfigMap → /checkout 200 闭环通过")
}

func getConfigMap(ctx context.Context, e *Environment, name string) (*corev1.ConfigMap, error) {
	var cm corev1.ConfigMap
	if err := e.K8s.Get(ctx, types.NamespacedName{Namespace: e.Namespace, Name: name}, &cm); err != nil {
		return nil, err
	}
	return &cm, nil
}
