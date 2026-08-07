package e2e

import (
	"context"
	"testing"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "k8s.io/api/apps/v1"
)

// TestE2EApprovalPatchMemory 场景 B:OOMKilled → PatchResourceLimit(ApprovalRequired)→ 审批 → memory limit 384Mi。
//
// 验证:
//  1. OOM 注入后发送 OOMKilled 告警 → incident 进入 AwaitingApproval
//  2. 提案为 PatchResourceLimit(container=faultlab, memoryLimit=384Mi,由 fake LLM 生成)
//  3. 使用 approver token 批准 → Executing → Resolved
//  4. Deployment 容器 memory limit 从 256Mi 变为 384Mi,且带 operation-id 注解
//  5. 审计链含 ApprovalGranted/ExecutionStarted/ExecutionCompleted/IncidentResolved
//  6. viewer token 调审批 → 403(角色边界)
func TestE2EApprovalPatchMemory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	e := testEnv(t)
	const alertName = "ContainerOOMKilled"
	fp := "sha256:e2e-oom-0001"
	incName := IncidentName(e, alertName, fp)

	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = RestoreFaultLab(c, e)
		_ = e.K8s.DeleteAllOf(c, &opsv1alpha1.AIOpsIncident{}, client.InNamespace(e.Namespace))
	})

	before, err := DeploymentSnapshot(ctx, e, e.Namespace, "faultlab")
	if err != nil {
		t.Fatal(err)
	}
	memBefore, _ := faultlabMemoryLimit(ctx, e, e.Namespace)
	t.Logf("初始 memory limit: %s", memBefore)

	if err := InjectOOMFault(ctx, e, 5*time.Minute); err != nil {
		t.Fatalf("注入 OOM: %v", err)
	}
	if err := WaitForOOMKilled(ctx, e, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	resp, err := PostAlert(ctx, e, map[string]string{
		"alertname": alertName,
		"namespace": e.Namespace,
		"workload":  "faultlab",
		"severity":  "critical",
	}, fp, "firing")
	if err != nil {
		t.Fatalf("发送告警: %v", err)
	}
	if resp.Rejected > 0 {
		t.Fatalf("告警被拒绝: %+v", resp)
	}

	if _, err := WaitIncidentCreated(ctx, e, e.Namespace, incName, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	inc, err := WaitIncidentPhase(ctx, e, e.Namespace, incName, opsv1alpha1.PhaseAwaitingApproval, 4*time.Minute)
	if err != nil {
		_ = DumpDiagnostics(ctx, e, t.TempDir(), e.Namespace, incName)
		t.Fatal(err)
	}
	if inc.Status.Proposal == nil || inc.Status.Proposal.Action != opsv1alpha1.ActionPatchResourceLimit {
		t.Fatalf("预期动作 PatchResourceLimit,实际 %+v", inc.Status.Proposal)
	}
	if inc.Status.Proposal.PlanDigest == "" {
		t.Fatal("proposal 缺 PlanDigest")
	}

	// viewer 角色调审批 → 403。
	if err := approveAs(ctx, e, e.Namespace, incName, e.ViewerToken); err == nil {
		t.Fatal("viewer token 审批应失败(403)")
	} else if !isForbidden(err) {
		t.Fatalf("viewer 审批错误应为 403,实际: %v", err)
	}

	if err := ApproveIncident(ctx, e, e.Namespace, incName, "e2e approve memory bump"); err != nil {
		t.Fatal(err)
	}
	_, err = WaitIncidentPhase(ctx, e, e.Namespace, incName, opsv1alpha1.PhaseResolved, 5*time.Minute)
	if err != nil {
		_ = DumpDiagnostics(ctx, e, t.TempDir(), e.Namespace, incName)
		t.Fatal(err)
	}
	var approvals opsv1alpha1.RemediationApprovalList
	if err := e.K8s.List(ctx, &approvals, client.InNamespace(e.Namespace)); err != nil {
		t.Fatalf("读取审批 CR: %v", err)
	}
	approved := false
	for _, approval := range approvals.Items {
		if approval.Spec.IncidentRef.Name == incName && approval.Spec.Decision == opsv1alpha1.ApprovalApprove {
			approved = true
			break
		}
	}
	if !approved {
		t.Fatalf("未找到已批准的 RemediationApproval(incident=%s)", incName)
	}

	memAfter, err := faultlabMemoryLimit(ctx, e, e.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if memAfter.Cmp(resource.MustParse("384Mi")) != 0 {
		t.Fatalf("预期 memory limit 384Mi,实际 %s", memAfter.String())
	}
	ds, err := DeploymentSnapshot(ctx, e, e.Namespace, "faultlab")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Annotations["ops.aegis.io/operation-id"] == "" {
		t.Fatal("Deployment 缺 operation-id 注解")
	}
	_ = before

	tr, err := QueryAuditTimeline(ctx, e, e.Namespace, incName)
	if err != nil {
		t.Fatal(err)
	}
	assertTimelineTypes(t, tr, "ApprovalGranted", "ExecutionStarted", "ExecutionCompleted", "IncidentResolved")

	t.Logf("场景 B 通过:审批闭环,memory limit %s → %s", memBefore, memAfter)
}

func faultlabMemoryLimit(ctx context.Context, e *Environment, ns string) (*resource.Quantity, error) {
	var d appsv1.Deployment
	if err := e.K8s.Get(ctx, types.NamespacedName{Namespace: ns, Name: "faultlab"}, &d); err != nil {
		return nil, err
	}
	for _, ctr := range d.Spec.Template.Spec.Containers {
		if q, ok := ctr.Resources.Limits["memory"]; ok {
			copied := q.DeepCopy()
			return &copied, nil
		}
	}
	return nil, nil
}
