package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/policy"
)

// policyTargetDeployment 是带版本/副本信息的目标。
func managedNamespace() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "fault-lab",
			Labels: map[string]string{"aegisops.io/managed": "true"},
		},
	}
}

func policyTargetDeployment() *appsv1.Deployment {
	replicas := int32(2)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "checkout-api", Namespace: "fault-lab", UID: "dep-uid-1",
			ResourceVersion: "rv-100",
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": "5"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "checkout"}},
		},
	}
}

func policyCR() *opsv1alpha1.RemediationPolicy {
	policy := &opsv1alpha1.RemediationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "fault-lab-default", Namespace: "fault-lab", UID: "pol-uid-1", Generation: 1},
		Spec: opsv1alpha1.RemediationPolicySpec{
			MaxAttemptsPerIncident: 2,
			VerificationWindow:     &metav1.Duration{Duration: 3 * time.Minute},
			Actions: map[opsv1alpha1.ActionType]opsv1alpha1.ActionPolicy{
				opsv1alpha1.ActionRestartWorkload: {Enabled: true, Mode: opsv1alpha1.ModeAuto},
				opsv1alpha1.ActionPatchResourceLimit: {
					Enabled:            true,
					Mode:               opsv1alpha1.ModeApprovalRequired,
					MaxMemory:          func() *opsv1alpha1.ResourceQuantity { q := opsv1alpha1.ResourceQuantity("1Gi"); return &q }(),
					MaxIncreasePercent: func() *int32 { v := int32(200); return &v }(),
				},
			},
		},
	}
	return policy
}

func policyCheckingIncident(action opsv1alpha1.ActionType, paramsJSON map[string]any) *opsv1alpha1.AIOpsIncident {
	i := firingIncident()
	i.UID = types.UID("uid-1")
	i.Finalizers = []string{FinalizerName}
	i.Spec.TargetRef.UID = "dep-uid-1"
	i.Status.Phase = opsv1alpha1.PhasePolicyChecking
	i.Status.Evidence = &opsv1alpha1.EvidenceSummary{Hash: "h"}
	i.Status.Diagnosis = &opsv1alpha1.DiagnosisSummary{Category: "CrashLoop", EvidenceIDs: []string{"e1"}}
	raw, _ := json.Marshal(paramsJSON)
	i.Status.Proposal = &opsv1alpha1.ActionProposal{
		Revision:    1,
		Action:      action,
		Parameters:  apiextensionsv1.JSON{Raw: raw},
		GeneratedAt: metav1.NewTime(time.Now()),
	}
	return i
}

func TestReconcile_PolicyCheckingAuto(t *testing.T) {
	incident := policyCheckingIncident(opsv1alpha1.ActionRestartWorkload, map[string]any{"reason": "CrashLoopBackOff 持续"})
	r, c := newReconciler(t, nil, incident, managedNamespace(), policyTargetDeployment(), policyCR())

	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter > 0 {
		t.Errorf("Auto 放行不应 requeue: %v", res.RequeueAfter)
	}

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseExecuting {
		t.Errorf("Auto 应转 Executing: %s", got.Status.Phase)
	}
	if got.Status.PolicyDecision == nil || got.Status.PolicyDecision.Decision != "Auto" {
		t.Errorf("策略判定未写入: %+v", got.Status.PolicyDecision)
	}
	if got.Status.PolicyDecision == nil || got.Status.PolicyDecision.VerificationWindow == nil || got.Status.PolicyDecision.VerificationWindow.Duration != 3*time.Minute {
		t.Errorf("策略验证窗口未冻结: %+v", got.Status.PolicyDecision)
	}
	if got.Status.Proposal.PlanDigest == "" {
		t.Error("PlanDigest 应被计算")
	}
}

func TestWritePolicyDecision_PreservesFrozenVerificationWindowOnDeny(t *testing.T) {
	incident := policyCheckingIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "app", "memoryLimit": "512Mi"})
	incident.Status.PolicyDecision = &opsv1alpha1.PolicyDecisionStatus{
		VerificationWindow: &metav1.Duration{Duration: 3 * time.Minute},
	}

	writePolicyDecision(incident, policy.Decision{Type: policy.DecisionDeny}, policyCR())

	if incident.Status.PolicyDecision.VerificationWindow == nil || incident.Status.PolicyDecision.VerificationWindow.Duration != 3*time.Minute {
		t.Fatalf("Deny 不得清空已冻结验证窗口: %+v", incident.Status.PolicyDecision)
	}
}

func TestReconcile_PolicyCheckingApprovalRequired(t *testing.T) {
	incident := policyCheckingIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "app", "memoryLimit": "512Mi"})
	r, c := newReconciler(t, nil, incident, managedNamespace(), policyTargetDeployment(), policyCR())

	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter != 15*time.Second {
		t.Errorf("应 requeue 15s 等审批: %v", res.RequeueAfter)
	}

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseAwaitingApproval {
		t.Errorf("中风险应转 AwaitingApproval: %s", got.Status.Phase)
	}
}

func TestReconcile_PolicyCheckingDenied(t *testing.T) {
	// 资源超限 → Deny → Escalated。
	incident := policyCheckingIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "app", "memoryLimit": "2Gi"})
	r, c := newReconciler(t, nil, incident, managedNamespace(), policyTargetDeployment(), policyCR())

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("Deny 应转 Escalated: %s", got.Status.Phase)
	}
	if got.Status.PolicyDecision == nil || got.Status.PolicyDecision.Decision != "Deny" {
		t.Errorf("Deny 判定未写入: %+v", got.Status.PolicyDecision)
	}
	if c := got.GetCondition("PolicyChecked"); c == nil || c.Status != metav1.ConditionFalse {
		t.Error("应设置 PolicyChecked=False")
	}
}

func TestReconcile_PolicyCheckingNoPolicy(t *testing.T) {
	incident := policyCheckingIncident(opsv1alpha1.ActionRestartWorkload, map[string]any{"reason": "x"})
	r, c := newReconciler(t, nil, incident, managedNamespace(), policyTargetDeployment())

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("无策略应 Escalated: %s", got.Status.Phase)
	}
}

func TestReconcile_PolicyCheckingNoProposal(t *testing.T) {
	incident := policyCheckingIncident(opsv1alpha1.ActionRestartWorkload, map[string]any{})
	incident.Status.Proposal = nil
	r, c := newReconciler(t, nil, incident, managedNamespace(), policyTargetDeployment(), policyCR())

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseRecoveredNoAction {
		t.Errorf("无方案应 RecoveredWithoutAction: %s", got.Status.Phase)
	}
}

func TestReconcile_AwaitingApprovalGranted(t *testing.T) {
	incident := policyCheckingIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "app", "memoryLimit": "512Mi"})
	incident.Status.Phase = opsv1alpha1.PhaseAwaitingApproval

	// 预计算摘要。
	digest, err := policy.BuildPlanDigest(policy.DigestInput{
		IncidentUID:           incident.UID,
		Target:                incident.Spec.TargetRef,
		TargetResourceVersion: "rv-100",
		Action:                incident.Status.Proposal.Action,
		Parameters:            map[string]any{"container": "app", "memoryLimit": "512Mi"},
		PolicyUID:             types.UID("pol-uid-1"),
		PolicyGeneration:      1,
	})
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	incident.Status.Proposal.PlanDigest = digest

	approval := &opsv1alpha1.RemediationApproval{
		ObjectMeta: metav1.ObjectMeta{Name: "inc-1-approval", Namespace: "fault-lab", CreationTimestamp: metav1.Now()},
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{Name: "incident-1", UID: incident.UID, ProposalRevision: 1},
			Decision:    opsv1alpha1.ApprovalApprove,
			PlanDigest:  digest,
			Actor:       "console-approver",
			Reason:      "确认",
			ExpiresAt:   metav1.NewTime(time.Now().Add(10 * time.Minute)),
		},
	}
	r, c := newReconciler(t, nil, incident, managedNamespace(), policyTargetDeployment(), policyCR(), approval)

	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter > 0 {
		t.Errorf("审批通过不应 requeue: %v", res.RequeueAfter)
	}
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseExecuting {
		t.Errorf("审批通过应转 Executing: %s", got.Status.Phase)
	}
	if got.Status.Approval == nil || got.Status.Approval.Actor != "console-approver" {
		t.Errorf("审批状态未写入: %+v", got.Status.Approval)
	}
}

func TestReconcile_AwaitingApprovalDigestTampered(t *testing.T) {
	incident := policyCheckingIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "app", "memoryLimit": "512Mi"})
	incident.UID = types.UID("uid-1")
	incident.Status.Phase = opsv1alpha1.PhaseAwaitingApproval
	// 伪造摘要。
	incident.Status.Proposal.PlanDigest = "sha256:" + repeatChar('f', 64)

	approval := &opsv1alpha1.RemediationApproval{
		ObjectMeta: metav1.ObjectMeta{Name: "inc-1-approval", Namespace: "fault-lab", CreationTimestamp: metav1.Now()},
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{Name: "incident-1", UID: incident.UID, ProposalRevision: 1},
			Decision:    opsv1alpha1.ApprovalApprove,
			PlanDigest:  "sha256:" + repeatChar('f', 64),
			Actor:       "x",
			Reason:      "x",
			ExpiresAt:   metav1.NewTime(time.Now().Add(10 * time.Minute)),
		},
	}
	r, c := newReconciler(t, nil, incident, managedNamespace(), policyTargetDeployment(), policyCR(), approval)

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase == opsv1alpha1.PhaseExecuting {
		t.Error("摘要篡改不应执行")
	}
	// 保持 AwaitingApproval 等待新审批（不硬 Escalated，允许人工重新审批）。
	if got.Status.Phase != opsv1alpha1.PhaseAwaitingApproval {
		t.Errorf("摘要不匹配应保持等待: %s", got.Status.Phase)
	}
}

func TestReconcile_AwaitingApprovalRejected(t *testing.T) {
	incident := policyCheckingIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "app", "memoryLimit": "512Mi"})
	incident.UID = types.UID("uid-1")
	incident.Status.Phase = opsv1alpha1.PhaseAwaitingApproval
	incident.Status.Proposal.PlanDigest = "sha256:" + repeatChar('a', 64)

	approval := &opsv1alpha1.RemediationApproval{
		ObjectMeta: metav1.ObjectMeta{Name: "inc-1-approval", Namespace: "fault-lab", CreationTimestamp: metav1.Now()},
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{Name: "incident-1", UID: incident.UID, ProposalRevision: 1},
			Decision:    opsv1alpha1.ApprovalReject,
			PlanDigest:  "sha256:" + repeatChar('a', 64),
			Actor:       "console-approver",
			Reason:      "风险不可接受",
			ExpiresAt:   metav1.NewTime(time.Now().Add(10 * time.Minute)),
		},
	}
	r, c := newReconciler(t, nil, incident, managedNamespace(), policyTargetDeployment(), policyCR(), approval)

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("拒绝应转 Escalated: %s", got.Status.Phase)
	}
	if got.Status.Approval == nil || got.Status.Approval.Decision != "Reject" {
		t.Errorf("拒绝状态未写入: %+v", got.Status.Approval)
	}
}

func TestReconcile_AwaitingApprovalNone(t *testing.T) {
	incident := policyCheckingIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "app", "memoryLimit": "512Mi"})
	incident.Status.Phase = opsv1alpha1.PhaseAwaitingApproval
	incident.Status.Proposal.PlanDigest = "sha256:" + repeatChar('a', 64)
	r, _ := newReconciler(t, nil, incident, managedNamespace(), policyTargetDeployment(), policyCR())

	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter != 15*time.Second {
		t.Errorf("无审批应 requeue 15s: %v", res.RequeueAfter)
	}
}

func TestReconcile_PolicyCheckingSuggestOnly(t *testing.T) {
	incident := policyCheckingIncident(opsv1alpha1.ActionRestartWorkload, map[string]any{"reason": "CrashLoopBackOff 持续"})
	policyCR := policyCR()
	setPolicyMode(policyCR, opsv1alpha1.ActionRestartWorkload, opsv1alpha1.ModeSuggestOnly)
	r, c := newReconciler(t, nil, incident, managedNamespace(), policyTargetDeployment(), policyCR)

	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter <= 0 {
		t.Errorf("SuggestOnly 应保持 requeue: %v", res.RequeueAfter)
	}
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhasePolicyChecking {
		t.Errorf("SuggestOnly 不应改变阶段: %s", got.Status.Phase)
	}
	if got.Status.PolicyDecision == nil || got.Status.PolicyDecision.Decision != "SuggestOnly" {
		t.Errorf("SuggestOnly 判定未写入: %+v", got.Status.PolicyDecision)
	}
}

func setPolicyMode(p *opsv1alpha1.RemediationPolicy, action opsv1alpha1.ActionType, mode opsv1alpha1.PolicyMode) {
	ap := p.Spec.Actions[action]
	ap.Mode = mode
	p.Spec.Actions[action] = ap
}

func TestReconcile_TargetMissingInPolicy(t *testing.T) {
	// PolicyChecking 阶段目标被删除 → Escalated。
	incident := policyCheckingIncident(opsv1alpha1.ActionRestartWorkload, map[string]any{"reason": "x"})
	r, c := newReconciler(t, nil, incident, managedNamespace(), policyCR())

	reconcileOnce(t, r, "incident-1")
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("目标缺失应 Escalated: %s", got.Status.Phase)
	}
}

// TestReconcile_AwaitingApprovalRVToggle_RefreshesDigest
// 缺陷回归:审批等待期目标 RV 变化 → digest 校验失败。
// 期望:Proposal.PlanDigest 刷新为新 RV 的摘要(旧审批失效),流程可恢复。
func TestReconcile_AwaitingApprovalRVChanged_RefreshesDigest(t *testing.T) {
	incident := policyCheckingIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "app", "memoryLimit": "512Mi"})
	incident.UID = types.UID("uid-1")
	incident.Status.Phase = opsv1alpha1.PhaseAwaitingApproval

	// PolicyChecking 时绑定 RV-100 的旧摘要。
	oldDigest, err := policy.BuildPlanDigest(policy.DigestInput{
		IncidentUID:           incident.UID,
		Target:                incident.Spec.TargetRef,
		TargetResourceVersion: "rv-100",
		Action:                incident.Status.Proposal.Action,
		Parameters:            map[string]any{"container": "app", "memoryLimit": "512Mi"},
		PolicyUID:             types.UID("pol-uid-1"),
		PolicyGeneration:      1,
	})
	if err != nil {
		t.Fatalf("old digest: %v", err)
	}
	incident.Status.Proposal.PlanDigest = oldDigest

	// 审批绑定旧摘要(审批人当时批准的版本)。
	approval := &opsv1alpha1.RemediationApproval{
		ObjectMeta: metav1.ObjectMeta{Name: "inc-1-approval", Namespace: "fault-lab", CreationTimestamp: metav1.Now()},
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{Name: "incident-1", UID: incident.UID, ProposalRevision: 1},
			Decision:    opsv1alpha1.ApprovalApprove,
			PlanDigest:  oldDigest,
			Actor:       "console-approver",
			Reason:      "确认",
			ExpiresAt:   metav1.NewTime(time.Now().Add(10 * time.Minute)),
		},
	}

	// 目标 RV 已变化(rv-200),模拟 rollout。
	dep := policyTargetDeployment()
	dep.ResourceVersion = "rv-200"
	r, c := newReconciler(t, nil, incident, managedNamespace(), dep, policyCR(), approval)

	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter != 15*time.Second {
		t.Errorf("应 requeue 15s 等待新审批: %v", res.RequeueAfter)
	}

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseAwaitingApproval {
		t.Fatalf("应保持 AwaitingApproval: %s", got.Status.Phase)
	}
	// 关键断言:digest 必须被刷新(绑定新 RV),不再等于旧摘要。
	if got.Status.Proposal.PlanDigest == oldDigest {
		t.Error("Proposal.PlanDigest 应被刷新为新 RV 的摘要（恢复路径）")
	}
	newDigest := got.Status.Proposal.PlanDigest
	// 新摘要必须能通过基于新 RV 的校验。
	if err := policy.VerifyPlanDigest(newDigest, policy.DigestInput{
		IncidentUID:           got.UID,
		Target:                got.Spec.TargetRef,
		TargetResourceVersion: "rv-200",
		Action:                got.Status.Proposal.Action,
		Parameters:            map[string]any{"container": "app", "memoryLimit": "512Mi"},
		PolicyUID:             types.UID("pol-uid-1"),
		PolicyGeneration:      1,
	}); err != nil {
		t.Errorf("刷新后的摘要应匹配新 RV: %v", err)
	}

	// 恢复路径:审批人基于新 digest 创建新审批 → 应能通过并转 Executing。
	newApproval := &opsv1alpha1.RemediationApproval{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "inc-1-approval-2",
			Namespace:         "fault-lab",
			CreationTimestamp: metav1.NewTime(time.Now().Add(time.Minute)), // 晚于旧审批
		},
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{Name: "incident-1", UID: incident.UID, ProposalRevision: 1},
			Decision:    opsv1alpha1.ApprovalApprove,
			PlanDigest:  newDigest,
			Actor:       "console-approver",
			Reason:      "目标已变化，重新确认",
			ExpiresAt:   metav1.NewTime(time.Now().Add(10 * time.Minute)),
		},
	}
	if err := c.Create(context.Background(), newApproval); err != nil {
		t.Fatalf("创建新审批: %v", err)
	}

	reconcileOnce(t, r, "incident-1")
	got = opsv1alpha1.AIOpsIncident{}
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseExecuting {
		t.Errorf("新审批通过后应转 Executing: %s", got.Status.Phase)
	}
}

func TestReconcile_AwaitingApprovalExpiredKeepsWaiting(t *testing.T) {
	// 审批已过期 → 刷新摘要并保持等待（可用性恢复路径，不回滚旧审批语义）。
	incident := policyCheckingIncident(opsv1alpha1.ActionPatchResourceLimit, map[string]any{"container": "app", "memoryLimit": "512Mi"})
	incident.UID = types.UID("uid-1")
	incident.Status.Phase = opsv1alpha1.PhaseAwaitingApproval
	incident.Status.Proposal.PlanDigest = "sha256:" + repeatChar('a', 64)

	approval := &opsv1alpha1.RemediationApproval{
		ObjectMeta: metav1.ObjectMeta{Name: "inc-1-approval", Namespace: "fault-lab", CreationTimestamp: metav1.Now()},
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{Name: "incident-1", UID: incident.UID, ProposalRevision: 1},
			Decision:    opsv1alpha1.ApprovalApprove,
			PlanDigest:  "sha256:" + repeatChar('a', 64),
			Actor:       "x",
			Reason:      "x",
			ExpiresAt:   metav1.NewTime(time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)), // 已过期
		},
	}
	r, c := newReconciler(t, nil, incident, managedNamespace(), policyTargetDeployment(), policyCR(), approval)

	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter != 15*time.Second {
		t.Errorf("过期应保持等待 requeue 15s: %v", res.RequeueAfter)
	}
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), keyIncident(), &got)
	if got.Status.Phase != opsv1alpha1.PhaseAwaitingApproval {
		t.Errorf("过期审批应保持等待: %s", got.Status.Phase)
	}
}
