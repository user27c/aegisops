package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestE2EAutoRestart 场景 A:CheckoutHTTP500s → RestartWorkload(Auto)→ Resolved → 应用恢复。
//
// 验证:
//  1. 注入 config 故障后 /checkout 500
//  2. 告警触发 incident,决策 Auto 且动作 RestartWorkload
//  3. 执行后 Deployment 带 operation-id/restarted-at 注解,应用恢复 200
//  4. 审计时间线含 ExecutionStarted/ExecutionCompleted/IncidentResolved
//  5. 相同告警去重,不创建第二个 incident
//  6. 重启 Operator 后不重复执行(幂等)
func TestE2EAutoRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	e := testEnv(t)
	const alertName = "CheckoutHTTP500s"
	fp := "sha256:e2e-autorestart-0001"
	incName := IncidentName(e, alertName, fp)

	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = RestoreFaultLab(c, e)
		_ = e.K8s.DeleteAllOf(c, &opsv1alpha1.AIOpsIncident{}, client.InNamespace(e.Namespace))
	})

	if err := waitFor(ctx, 2*time.Minute, func() (bool, string) {
		if CheckoutHealthy(ctx, e) {
			return true, ""
		}
		return false, "fault-lab /checkout 尚未恢复"
	}); err != nil {
		t.Fatalf("前置失败:fault-lab /checkout 应初始为 200: %v", err)
	}
	if err := InjectFault(ctx, e, "config", 10*time.Minute); err != nil {
		t.Fatalf("注入 config 故障: %v", err)
	}
	if CheckoutHealthy(ctx, e) {
		t.Fatal("注入后 /checkout 仍 200,故障未生效")
	}

	resp, err := PostAlert(ctx, e, map[string]string{
		"alertname": alertName,
		"namespace": e.Namespace,
		"workload":  "faultlab",
		"severity":  "critical",
		"cluster":   "kind-e2e",
	}, fp, "firing")
	if err != nil {
		t.Fatalf("发送告警: %v", err)
	}
	if resp.Rejected > 0 || resp.Accepted < 1 {
		t.Fatalf("告警未被接受: %+v", resp)
	}

	inc, err := WaitIncidentCreated(ctx, e, e.Namespace, incName, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("incident 创建: %s phase=%s", inc.Name, inc.Status.Phase)

	// Incident 尚未终态时重发相同告警：必须去重，不能创建并行 episode。
	resp2, err := PostAlert(ctx, e, map[string]string{
		"alertname": alertName,
		"namespace": e.Namespace,
		"workload":  "faultlab",
		"severity":  "critical",
	}, fp, "firing")
	if err != nil {
		t.Fatalf("重发告警: %v", err)
	}
	if resp2.Deduplicated < 1 {
		t.Fatalf("预期去重,实际 %+v", resp2)
	}

	inc, err = WaitIncidentPhase(ctx, e, e.Namespace, incName, opsv1alpha1.PhaseResolved, 5*time.Minute)
	if err != nil {
		_ = DumpDiagnostics(ctx, e, t.TempDir(), e.Namespace, incName)
		t.Fatal(err)
	}

	if inc.Status.Proposal == nil || inc.Status.Proposal.Action != opsv1alpha1.ActionRestartWorkload {
		t.Fatalf("预期动作 RestartWorkload,实际 %+v", inc.Status.Proposal)
	}
	if inc.Status.Evidence == nil || inc.Status.Evidence.Counts["LogExcerpt"] < 1 {
		t.Fatalf("证据包应包含 Loki LogExcerpt(operator 必须经 LOKI_URL 采集日志): %+v", inc.Status.Evidence)
	}
	if inc.Status.PolicyDecision == nil || inc.Status.PolicyDecision.Decision != "Auto" {
		t.Fatalf("预期决策 Auto,实际 %+v", inc.Status.PolicyDecision)
	}
	if inc.Status.Execution == nil || inc.Status.Execution.Reference == nil || inc.Status.Execution.Reference.OperationID == "" {
		t.Fatalf("缺少 OperationID: %+v", inc.Status.Execution)
	}

	if err := waitFor(ctx, 2*time.Minute, func() (bool, string) {
		if CheckoutHealthy(ctx, e) {
			return true, ""
		}
		return false, "等待 RestartWorkload 后 /checkout 恢复 200"
	}); err != nil {
		t.Fatal(err)
	}
	ds, err := DeploymentSnapshot(ctx, e, e.Namespace, "faultlab")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Annotations["ops.aegis.io/operation-id"] == "" {
		t.Fatal("Deployment 缺 ops.aegis.io/operation-id 注解")
	}
	if ds.TemplateAnnotations["ops.aegis.io/restarted-at"] == "" {
		t.Fatal("PodTemplate 缺 ops.aegis.io/restarted-at 注解")
	}

	tr, err := QueryAuditTimeline(ctx, e, e.Namespace, incName)
	if err != nil {
		t.Fatal(err)
	}
	assertTimelineTypes(t, tr, "ExecutionStarted", "ExecutionCompleted", "IncidentResolved")

	// 重启 Operator:不应重复 Apply(operation-id 不变)。
	if err := restartDeployment(ctx, e, e.SystemNamespace, "aegisops-operator"); err != nil {
		t.Fatal(err)
	}
	if err := waitFor(ctx, 3*time.Minute, func() (bool, string) {
		ds2, err := DeploymentSnapshot(ctx, e, e.Namespace, "faultlab")
		if err != nil {
			return false, err.Error()
		}
		return ds2.Annotations["ops.aegis.io/operation-id"] == ds.Annotations["ops.aegis.io/operation-id"], "operation-id 应保持不变"
	}); err != nil {
		t.Fatal(err)
	}

	t.Log("场景 A 通过:Auto 重启闭环 + 去重 + operator 重启幂等")
}

func assertTimelineTypes(t *testing.T, tr TimelineResponse, want ...string) {
	t.Helper()
	got := map[string]bool{}
	for _, it := range tr.Items {
		got[it.Type] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("时间线缺少事件 %q(实际 %v)", w, tr.Items)
		}
	}
}

func restartDeployment(ctx context.Context, e *Environment, ns, name string) error {
	var d appsv1.Deployment
	if err := e.K8s.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &d); err != nil {
		return err
	}
	if d.Spec.Template.Annotations == nil {
		d.Spec.Template.Annotations = map[string]string{}
	}
	d.Spec.Template.Annotations["ops.aegis.io/e2e-restart"] = fmt.Sprintf("%d", time.Now().UnixNano())
	return e.K8s.Update(ctx, &d)
}

var _ = metav1.Now
