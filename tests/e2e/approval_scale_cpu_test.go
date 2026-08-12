package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestE2EApprovalScaleCPUFailClosed 证明两件事：
//  1. ScaleDeployment 的 Apply 真实扩容（spec.replicas 从 1 升到 proposal 指定值）；
//  2. CPU 单 Pod 故障不能因副本 Ready 被错误标为已修复：ScaleDeployment 必须
//     读取 Prometheus 限流比例，持续超阈值时进入回滚而非 Resolved，且 Rollback
//     真实还原 spec.replicas 到 1。
//
// 该场景是受控负向 E2E，不把无因果的扩容宣传为自愈。
func TestE2EApprovalScaleCPUFailClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	e := testEnv(t)
	const alertName = "ContainerCPUThrottlingHigh"
	const fp = "sha256:e2e-cpu-scale-0001"
	incName := IncidentName(e, alertName, fp)

	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = RestoreFaultLab(c, e)
		_ = e.K8s.DeleteAllOf(c, &opsv1alpha1.AIOpsIncident{}, client.InNamespace(e.Namespace))
	})

	// 记录原始副本数作为 Apply 扩容与 Rollback 还原的断言基线。
	origReplicas, err := faultlabSpecReplicas(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if origReplicas != 1 {
		t.Fatalf("前置 faultlab spec.replicas=%d, want 1", origReplicas)
	}

	if err := InjectFault(ctx, e, "cpu", 5*time.Minute); err != nil {
		t.Fatalf("注入 CPU: %v", err)
	}
	resp, err := PostAlert(ctx, e, map[string]string{
		"alertname": alertName,
		"namespace": e.Namespace,
		"workload":  "faultlab",
		"severity":  "critical",
	}, fp, "firing")
	if err != nil || resp.Rejected > 0 {
		t.Fatalf("发送 CPU 告警: resp=%+v err=%v", resp, err)
	}
	inc, err := WaitIncidentPhase(ctx, e, e.Namespace, incName, opsv1alpha1.PhaseAwaitingApproval, 4*time.Minute)
	if err != nil {
		_ = DumpDiagnostics(ctx, e, t.TempDir(), e.Namespace, incName)
		t.Fatal(err)
	}
	if inc.Status.Proposal == nil || inc.Status.Proposal.Action != opsv1alpha1.ActionScaleDeployment {
		t.Fatalf("预期 ScaleDeployment,实际 %+v", inc.Status.Proposal)
	}
	var params map[string]any
	if err := json.Unmarshal(inc.Status.Proposal.Parameters.Raw, &params); err != nil {
		t.Fatalf("解析 ScaleDeployment 参数: %v", err)
	}
	replicasVal, ok := params["replicas"].(float64)
	if !ok || replicasVal <= float64(origReplicas) {
		t.Fatalf("ScaleDeployment 参数 replicas=%v 未体现扩容(原始 %d)", params["replicas"], origReplicas)
	}
	targetReplicas := int32(replicasVal)

	if err := ApproveIncident(ctx, e, e.Namespace, incName, "e2e approve CPU scale"); err != nil {
		t.Fatal(err)
	}
	// 证明 Apply 真实扩容：spec.replicas 从 1 升到 proposal 指定值。
	if err := waitFaultlabSpecReplicas(ctx, e, targetReplicas, 2*time.Minute); err != nil {
		_ = DumpDiagnostics(ctx, e, t.TempDir(), e.Namespace, incName)
		t.Fatalf("ScaleDeployment Apply 未真实扩容到 %d: %v", targetReplicas, err)
	}
	// Policy verificationWindow 是 5 分钟；额外一分钟覆盖首次验证和
	// Reconcile 调度，避免测试与 fail-closed 回滚在同一时刻竞争超时。
	inc, err = WaitIncidentPhase(ctx, e, e.Namespace, incName, opsv1alpha1.PhaseRolledBack, 6*time.Minute)
	if err != nil {
		_ = DumpDiagnostics(ctx, e, t.TempDir(), e.Namespace, incName)
		t.Fatal(err)
	}
	if inc.Status.Verification == nil || inc.Status.Verification.State != "Unhealthy" {
		t.Fatalf("CPU 指标未恢复时必须保持 Unhealthy: %+v", inc.Status.Verification)
	}
	// 证明 Rollback 真实还原：spec.replicas 回到 1。
	if got, err := faultlabSpecReplicas(ctx, e); err != nil {
		t.Fatal(err)
	} else if got != origReplicas {
		t.Fatalf("回滚后 faultlab spec.replicas=%d, want %d", got, origReplicas)
	}
	t.Logf("场景 Scale CPU 通过: 副本 %d→%d→%d 真实变更并回滚, 指标未恢复时 fail-closed", origReplicas, targetReplicas, origReplicas)
}

// faultlabSpecReplicas 读取 faultlab Deployment 的 spec.replicas。
func faultlabSpecReplicas(ctx context.Context, e *Environment) (int32, error) {
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: e.Namespace, Name: "faultlab"}
	if err := e.K8s.Get(ctx, key, &d); err != nil {
		return 0, err
	}
	if d.Spec.Replicas == nil {
		return 0, fmt.Errorf("faultlab spec.replicas 为 nil")
	}
	return *d.Spec.Replicas, nil
}

// waitFaultlabSpecReplicas 轮询直到 faultlab spec.replicas 等于 want。
func waitFaultlabSpecReplicas(ctx context.Context, e *Environment, want int32, timeout time.Duration) error {
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: e.Namespace, Name: "faultlab"}
	return waitFor(ctx, timeout, func() (bool, string) {
		if err := e.K8s.Get(ctx, key, &d); err != nil {
			return false, err.Error()
		}
		if d.Spec.Replicas != nil && *d.Spec.Replicas == want {
			return true, ""
		}
		cur := int32(-1)
		if d.Spec.Replicas != nil {
			cur = *d.Spec.Replicas
		}
		return false, fmt.Sprintf("faultlab spec.replicas=%d, want %d", cur, want)
	})
}
