package e2e

import (
	"context"
	"testing"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestE2ERollbackDeployment 场景 C:ImagePullBackOff → RollbackDeployment(ApprovalRequired)→ 回滚镜像。
//
// 验证:
//  1. 将 faultlab 镜像改为不存在的镜像 → ImagePullBackOff
//  2. 发送 ImagePullBackOff 告警 → incident AwaitingApproval(提案 RollbackDeployment)
//  3. 批准 → 执行回滚(snapshot 恢复原镜像)→ Resolved
//  4. Deployment 镜像恢复原值且副本可用
//  5. 审计链完整
//
// 说明:verifier 失败自动回滚(RollingBack→RolledBack)需要真实 LLM 产出 ScaleDeployment
// 提案且执行后副本不可用,当前 fake LLM 无此路径,留 M9.7 评估时补充。
func TestE2ERollbackDeployment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	e := testEnv(t)
	const alertName = "ImagePullBackOff"
	fp := "sha256:e2e-rollback-0001"
	incName := IncidentName(e, alertName, fp)

	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = RestoreFaultLab(c, e)
		_ = e.K8s.DeleteAllOf(c, &opsv1alpha1.AIOpsIncident{}, client.InNamespace(e.Namespace))
	})

	var d appsv1.Deployment
	if err := e.K8s.Get(ctx, types.NamespacedName{Namespace: e.Namespace, Name: "faultlab"}, &d); err != nil {
		t.Fatal(err)
	}
	origImage := d.Spec.Template.Spec.Containers[0].Image
	t.Logf("原镜像: %s", origImage)

	// 前置场景会产生多个 Deployment revision；本场景验证回滚闭环，
	// 将测试策略上限放宽到 fixture 的目标 revision 1，避免历史 revision 数污染策略断言。
	var policy opsv1alpha1.RemediationPolicy
	policyKey := types.NamespacedName{Namespace: e.Namespace, Name: "fault-lab-default"}
	if err := e.K8s.Get(ctx, policyKey, &policy); err != nil {
		t.Fatal(err)
	}
	rollbackPolicy, ok := policy.Spec.Actions[opsv1alpha1.ActionRollbackDeployment]
	if !ok {
		t.Fatal("默认策略缺少 RollbackDeployment")
	}
	maxDistance := int64(10)
	rollbackPolicy.MaxRevisionDistance = &maxDistance
	policy.Spec.Actions[opsv1alpha1.ActionRollbackDeployment] = rollbackPolicy
	if err := e.K8s.Update(ctx, &policy); err != nil {
		t.Fatal(err)
	}

	// 改成不存在的镜像触发 ImagePullBackOff。
	patch := client.MergeFrom(d.DeepCopy())
	d.Spec.Template.Spec.Containers[0].Image = "nonexistent.invalid/e2e/nope:v9"
	if err := e.K8s.Patch(ctx, &d, patch); err != nil {
		t.Fatal(err)
	}
	if err := WaitForImagePullBackOff(ctx, e, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	defer func() {
		var dd appsv1.Deployment
		if e.K8s.Get(context.Background(), types.NamespacedName{Namespace: e.Namespace, Name: "faultlab"}, &dd) == nil {
			p := client.MergeFrom(dd.DeepCopy())
			dd.Spec.Template.Spec.Containers[0].Image = origImage
			_ = e.K8s.Patch(context.Background(), &dd, p)
		}
	}()

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
	if inc.Status.Proposal == nil || inc.Status.Proposal.Action != opsv1alpha1.ActionRollbackDeployment {
		t.Fatalf("预期动作 RollbackDeployment,实际 %+v", inc.Status.Proposal)
	}

	if err := ApproveIncident(ctx, e, e.Namespace, incName, "e2e approve rollback"); err != nil {
		t.Fatal(err)
	}
	_, err = WaitIncidentPhase(ctx, e, e.Namespace, incName, opsv1alpha1.PhaseResolved, 6*time.Minute)
	if err != nil {
		_ = DumpDiagnostics(ctx, e, t.TempDir(), e.Namespace, incName)
		t.Fatal(err)
	}

	// 回滚后镜像应恢复原值。
	if err := e.K8s.Get(ctx, types.NamespacedName{Namespace: e.Namespace, Name: "faultlab"}, &d); err != nil {
		t.Fatal(err)
	}
	if d.Spec.Template.Spec.Containers[0].Image != origImage {
		t.Fatalf("回滚后镜像 %q,预期 %q", d.Spec.Template.Spec.Containers[0].Image, origImage)
	}
	ds, err := DeploymentSnapshot(ctx, e, e.Namespace, "faultlab")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Annotations["ops.aegis.io/operation-id"] == "" {
		t.Fatal("Deployment 缺 operation-id 注解")
	}

	tr, err := QueryAuditTimeline(ctx, e, e.Namespace, incName)
	if err != nil {
		t.Fatal(err)
	}
	assertTimelineTypes(t, tr, "ApprovalGranted", "ExecutionStarted", "ExecutionCompleted", "IncidentResolved")

	t.Log("场景 C 通过:回滚执行闭环,镜像恢复原值")
}
