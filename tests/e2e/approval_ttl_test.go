package e2e

import (
	"context"
	"testing"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestE2EApprovalTTL 验证 Policy 的 ApprovalTTL 真正控制审批有效期：
// 把命中策略的 approvalTTL 改为 3m（非默认 10m），断言：
//  1. Incident 在 PolicyDecision 里冻结了 3m 的 ApprovalTTL；
//  2. 审批 CR 的 ExpiresAt ≈ CreationTimestamp + 3m（而非 10m）。
//
// 这直接对应「确定性策略控制审批窗口」的设计承诺，防止退化为硬编码默认值。
func TestE2EApprovalTTL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	e := testEnv(t)

	const shortTTL = 3 * time.Minute

	// 读取命中策略并临时缩短 approvalTTL，随后还原。
	policyKey := types.NamespacedName{Namespace: e.Namespace, Name: "fault-lab-default"}
	var policy opsv1alpha1.RemediationPolicy
	if err := e.K8s.Get(ctx, policyKey, &policy); err != nil {
		t.Fatalf("读取命中策略: %v", err)
	}
	originalTTL := policy.Spec.ApprovalTTL
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var current opsv1alpha1.RemediationPolicy
		if err := e.K8s.Get(c, policyKey, &current); err != nil {
			t.Logf("清理时读取策略失败: %v", err)
			return
		}
		before := current.DeepCopy()
		current.Spec.ApprovalTTL = originalTTL
		if err := e.K8s.Patch(c, &current, client.MergeFrom(before)); err != nil {
			t.Logf("清理时还原策略 approvalTTL 失败: %v", err)
		}
	})
	patch := policy.DeepCopy()
	patch.Spec.ApprovalTTL = &metav1.Duration{Duration: shortTTL}
	if err := e.K8s.Patch(ctx, patch, client.MergeFrom(&policy)); err != nil {
		t.Fatalf("设置短 approvalTTL: %v", err)
	}

	const alertName = "ContainerOOMKilled"
	fp := "sha256:e2e-approval-ttl-0001"
	incName := IncidentName(e, alertName, fp)

	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = RestoreFaultLab(c, e)
		_ = e.K8s.DeleteAllOf(c, &opsv1alpha1.AIOpsIncident{}, client.InNamespace(e.Namespace))
		_ = e.K8s.DeleteAllOf(c, &opsv1alpha1.RemediationApproval{}, client.InNamespace(e.Namespace))
	})

	if err := InjectOOMFault(ctx, e, 5*time.Minute); err != nil {
		t.Fatalf("注入 OOM: %v", err)
	}
	if err := WaitForOOMKilled(ctx, e, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := WaitFaultLabHealthy(ctx, e, 2*time.Minute); err != nil {
		t.Fatalf("OOM 后 faultlab 未恢复稳定: %v", err)
	}
	if _, err := PostAlert(ctx, e, map[string]string{
		"alertname": alertName,
		"namespace": e.Namespace,
		"workload":  "faultlab",
		"severity":  "critical",
	}, fp, "firing"); err != nil {
		t.Fatalf("发送告警: %v", err)
	}

	if _, err := WaitIncidentCreated(ctx, e, e.Namespace, incName, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	inc, err := WaitIncidentPhase(ctx, e, e.Namespace, incName, opsv1alpha1.PhaseAwaitingApproval, 4*time.Minute)
	if err != nil {
		_ = DumpDiagnostics(ctx, e, t.TempDir(), e.Namespace, incName)
		t.Fatal(err)
	}

	// 断言 1：Incident 冻结了策略的短 ApprovalTTL。
	pd := inc.Status.PolicyDecision
	if pd == nil || pd.ApprovalTTL == nil || pd.ApprovalTTL.Duration != shortTTL {
		t.Fatalf("Incident 未冻结短 approvalTTL(期望 %s): decision=%+v", shortTTL, pd)
	}

	// 批准并等待完成。
	if err := ApproveIncident(ctx, e, e.Namespace, incName, "e2e approval ttl"); err != nil {
		t.Fatal(err)
	}
	if _, err := WaitIncidentPhase(ctx, e, e.Namespace, incName, opsv1alpha1.PhaseResolved, 5*time.Minute); err != nil {
		_ = DumpDiagnostics(ctx, e, t.TempDir(), e.Namespace, incName)
		t.Fatal(err)
	}

	// 断言 2：审批 CR 的 ExpiresAt ≈ CreationTimestamp + shortTTL。
	var approvals opsv1alpha1.RemediationApprovalList
	if err := e.K8s.List(ctx, &approvals, client.InNamespace(e.Namespace)); err != nil {
		t.Fatalf("读取审批 CR: %v", err)
	}
	var approved *opsv1alpha1.RemediationApproval
	for i := range approvals.Items {
		ap := &approvals.Items[i]
		if ap.Spec.IncidentRef.Name == incName && ap.Spec.Decision == opsv1alpha1.ApprovalApprove {
			approved = ap
			break
		}
	}
	if approved == nil {
		t.Fatalf("未找到已批准的 RemediationApproval(incident=%s)", incName)
	}
	gotTTL := approved.Spec.ExpiresAt.Sub(approved.CreationTimestamp.Time)
	// HTTP API 服务器与 kube-apiserver 时钟存在秒级偏差，容差 90s；
	// 但仍必须显著小于默认 10m，以证明 TTL 受 Policy 控制。
	if gotTTL < shortTTL-90*time.Second || gotTTL > shortTTL+90*time.Second {
		t.Fatalf("审批 ExpiresAt 未遵循短 TTL: 期望≈%s, 实际 %s (创建 %s → 过期 %s)",
			shortTTL, gotTTL, approved.CreationTimestamp.Time, approved.Spec.ExpiresAt.Time)
	}
	t.Logf("审批 TTL 通过: Policy 冻结 %s,审批实际有效期 %s", shortTTL, gotTTL)
}
