package policy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

func params(t *testing.T, v map[string]any) apiextensionsv1.JSON {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return apiextensionsv1.JSON{Raw: raw}
}

func testIncident() *opsv1alpha1.AIOpsIncident {
	now := metav1.Now()
	return &opsv1alpha1.AIOpsIncident{
		ObjectMeta: metav1.ObjectMeta{Name: "inc-1", Namespace: "fault-lab", UID: types.UID("uid-1")},
		Spec: opsv1alpha1.AIOpsIncidentSpec{
			TargetRef: opsv1alpha1.TargetReference{
				APIVersion: "apps/v1", Kind: "Deployment",
				Namespace: "fault-lab", Name: "checkout-api", UID: "dep-uid-1",
			},
			StartedAt: now,
		},
		Status: opsv1alpha1.AIOpsIncidentStatus{
			Diagnosis: &opsv1alpha1.DiagnosisSummary{EvidenceIDs: []string{"e1"}},
		},
	}
}

func restartPolicy() *opsv1alpha1.RemediationPolicy {
	return &opsv1alpha1.RemediationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "fault-lab", UID: types.UID("pol-uid-1"), Generation: 3},
		Spec: opsv1alpha1.RemediationPolicySpec{
			MaxAttemptsPerIncident: 2,
			Actions: map[opsv1alpha1.ActionType]opsv1alpha1.ActionPolicy{
				opsv1alpha1.ActionRestartWorkload: {
					Enabled: true,
					Mode:    opsv1alpha1.ModeAuto,
				},
				opsv1alpha1.ActionScaleDeployment: {
					Enabled:         true,
					Mode:            opsv1alpha1.ModeApprovalRequired,
					MaxReplicas:     int32Ptr(8),
					MaxReplicaDelta: int32Ptr(2),
				},
				opsv1alpha1.ActionPatchResourceLimit: {
					Enabled:            true,
					Mode:               opsv1alpha1.ModeApprovalRequired,
					MaxMemory:          quantityPtr("1Gi"),
					MaxIncreasePercent: int32Ptr(200),
				},
				opsv1alpha1.ActionRollbackDeployment: {
					Enabled:             true,
					Mode:                opsv1alpha1.ModeApprovalRequired,
					MaxRevisionDistance: int64Ptr(3),
				},
			},
		},
	}
}

func int32Ptr(v int32) *int32 { return &v }
func int64Ptr(v int64) *int64 { return &v }
func quantityPtr(v string) *opsv1alpha1.ResourceQuantity {
	q := opsv1alpha1.ResourceQuantity(v)
	return &q
}

func setActionEnabled(p *opsv1alpha1.RemediationPolicy, action opsv1alpha1.ActionType, enabled bool) {
	ap := p.Spec.Actions[action]
	ap.Enabled = enabled
	p.Spec.Actions[action] = ap
}

func setActionMode(p *opsv1alpha1.RemediationPolicy, action opsv1alpha1.ActionType, mode opsv1alpha1.PolicyMode) {
	ap := p.Spec.Actions[action]
	ap.Mode = mode
	p.Spec.Actions[action] = ap
}

func baseInput(proposal opsv1alpha1.ActionProposal) EvaluationInput {
	return EvaluationInput{
		Incident:           testIncident(),
		Proposal:           proposal,
		Policy:             restartPolicy(),
		Target:             ObjectInfo{UID: "dep-uid-1", ResourceVersion: "rv-1", Replicas: 2, Revision: 5},
		Now:                time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		AuditAvailable:     true,
		EvidenceSufficient: true,
	}
}

func restartProposal(t *testing.T) opsv1alpha1.ActionProposal {
	t.Helper()
	return opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionRestartWorkload,
		Parameters: params(t, map[string]any{"reason": "CrashLoopBackOff 持续"}),
	}
}

func TestEvaluate_AutoRestart(t *testing.T) {
	decision, err := (&DefaultEvaluator{}).Evaluate(context.Background(), baseInput(restartProposal(t)))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Type != DecisionAuto {
		t.Errorf("低风险重启应 Auto: %+v", decision)
	}
	if decision.Risk != opsv1alpha1.RiskLow {
		t.Errorf("风险应为 low: %s", decision.Risk)
	}
}

func TestEvaluate_UnknownActionDenied(t *testing.T) {
	proposal := restartProposal(t)
	proposal.Action = "DeleteNamespace"
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), baseInput(proposal))
	if !decision.Denied() || decision.Reasons[0].Code != ReasonActionNotRegistered {
		t.Errorf("未知动作应拒绝: %+v", decision)
	}
}

func TestEvaluate_NoPolicyDenied(t *testing.T) {
	in := baseInput(restartProposal(t))
	in.Policy = nil
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() || decision.Reasons[0].Code != ReasonTargetNotAllowed {
		t.Errorf("无策略应拒绝: %+v", decision)
	}
}

func TestEvaluate_ActionDisabledDenied(t *testing.T) {
	in := baseInput(restartProposal(t))
	setActionEnabled(in.Policy, opsv1alpha1.ActionRestartWorkload, false)
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() || decision.Reasons[0].Code != ReasonActionDisabled {
		t.Errorf("禁用动作应拒绝: %+v", decision)
	}
}

func TestEvaluate_MediumRiskRequiresApproval(t *testing.T) {
	proposal := opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionScaleDeployment,
		Parameters: params(t, map[string]any{"replicas": float64(4), "reason": "扩容"}),
	}
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), baseInput(proposal))
	if decision.Type != DecisionApprovalRequired {
		t.Errorf("中风险应要求审批: %+v", decision)
	}
}

func TestEvaluate_ScaleConstraintsViolated(t *testing.T) {
	// 超过 maxReplicas。
	proposal := opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionScaleDeployment,
		Parameters: params(t, map[string]any{"replicas": float64(10)}),
	}
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), baseInput(proposal))
	if !decision.Denied() || decision.Reasons[0].Code != ReasonConstraintsViolated {
		t.Errorf("超限应拒绝: %+v", decision)
	}

	// 单次 delta 超限。
	proposal = opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionScaleDeployment,
		Parameters: params(t, map[string]any{"replicas": float64(5)}), // 当前 2 → delta 3 > 2
	}
	decision, _ = (&DefaultEvaluator{}).Evaluate(context.Background(), baseInput(proposal))
	if !decision.Denied() || decision.Reasons[0].Code != ReasonConstraintsViolated {
		t.Errorf("delta 超限应拒绝: %+v", decision)
	}
}

func TestEvaluate_ResourcePatchConstraints(t *testing.T) {
	// 内存超上限。
	proposal := opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionPatchResourceLimit,
		Parameters: params(t, map[string]any{"container": "app", "memoryLimit": "2Gi"}),
	}
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), baseInput(proposal))
	if !decision.Denied() {
		t.Errorf("2Gi 超过 1Gi 上限应拒绝: %+v", decision)
	}

	// 合法 512Mi。
	proposal.Parameters = params(t, map[string]any{"container": "app", "memoryLimit": "512Mi"})
	decision, _ = (&DefaultEvaluator{}).Evaluate(context.Background(), baseInput(proposal))
	if decision.Type != DecisionApprovalRequired {
		t.Errorf("合法资源调整应要求审批: %+v", decision)
	}
}

func TestEvaluate_RollbackConstraints(t *testing.T) {
	// 回滚距离超限(5→1 = 4 > 3)。
	proposal := opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionRollbackDeployment,
		Parameters: params(t, map[string]any{"targetRevision": float64(1), "reason": "回滚"}),
	}
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), baseInput(proposal))
	if !decision.Denied() {
		t.Errorf("回滚距离超限应拒绝: %+v", decision)
	}

	// 合法回滚(5→3)。
	proposal.Parameters = params(t, map[string]any{"targetRevision": float64(3), "reason": "回滚"})
	decision, _ = (&DefaultEvaluator{}).Evaluate(context.Background(), baseInput(proposal))
	if decision.Type != DecisionApprovalRequired {
		t.Errorf("合法回滚应要求审批: %+v", decision)
	}
}

func TestEvaluate_AttemptsExceeded(t *testing.T) {
	in := baseInput(restartProposal(t))
	in.Target.Attempts = 2
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() || decision.Reasons[0].Code != ReasonAttemptsExceeded {
		t.Errorf("尝试超限应拒绝: %+v", decision)
	}
}

func TestEvaluate_CooldownActive(t *testing.T) {
	in := baseInput(restartProposal(t))
	last := time.Date(2026, 8, 1, 9, 55, 0, 0, time.UTC) // 5 分钟前
	in.Target.LastActionAt = &last
	in.Policy.Spec.Cooldown = &metav1.Duration{Duration: 10 * time.Minute}
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() || decision.Reasons[0].Code != ReasonCooldownActive {
		t.Errorf("冷却期内应拒绝: %+v", decision)
	}
}

func TestEvaluate_AuditUnavailable(t *testing.T) {
	in := baseInput(restartProposal(t))
	in.AuditAvailable = false
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() || decision.Reasons[0].Code != ReasonAuditUnavailable {
		t.Errorf("审计不可用应拒绝: %+v", decision)
	}
}

func TestEvaluate_EvidenceInsufficient(t *testing.T) {
	in := baseInput(restartProposal(t))
	in.EvidenceSufficient = false
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() || decision.Reasons[0].Code != ReasonEvidenceInsufficient {
		t.Errorf("证据不足应拒绝: %+v", decision)
	}
}

func TestEvaluate_ApprovalFlow(t *testing.T) {
	// 构造中风险 + 合法审批。
	proposal := opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionScaleDeployment,
		Parameters: params(t, map[string]any{"replicas": float64(4), "reason": "扩容"}),
	}
	digest, err := BuildPlanDigest(DigestInput{
		IncidentUID:           types.UID("uid-1"),
		Target:                testIncident().Spec.TargetRef,
		TargetResourceVersion: "rv-1",
		Action:                proposal.Action,
		Parameters:            map[string]any{"replicas": float64(4), "reason": "扩容"},
		PolicyUID:             types.UID("pol-uid-1"),
		PolicyGeneration:      3,
	})
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	proposal.PlanDigest = digest

	approval := &opsv1alpha1.RemediationApproval{
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{
				Name: "inc-1", UID: types.UID("uid-1"), ProposalRevision: 1,
			},
			Decision:   opsv1alpha1.ApprovalApprove,
			PlanDigest: digest,
			Actor:      "console-approver",
			Reason:     "确认扩容",
			ExpiresAt:  metav1.NewTime(time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC)),
		},
	}

	in := baseInput(proposal)
	in.Approval = approval
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if decision.Denied() {
		t.Errorf("合法审批应放行: %+v", decision)
	}
}

func TestEvaluate_ApprovalDigestTampered(t *testing.T) {
	proposal := opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionScaleDeployment,
		Parameters: params(t, map[string]any{"replicas": float64(4)}),
		PlanDigest: "sha256:" + strings.Repeat("f", 64), // 伪造摘要
	}
	approval := &opsv1alpha1.RemediationApproval{
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{Name: "inc-1", UID: types.UID("uid-1"), ProposalRevision: 1},
			Decision:    opsv1alpha1.ApprovalApprove,
			PlanDigest:  "sha256:" + strings.Repeat("f", 64),
			Actor:       "x",
			Reason:      "x",
			ExpiresAt:   metav1.NewTime(time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC)),
		},
	}
	in := baseInput(proposal)
	in.Approval = approval
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() || decision.Reasons[0].Code != ReasonApprovalMismatch {
		t.Errorf("摘要篡改应拒绝: %+v", decision)
	}
}

func TestEvaluate_ApprovalExpired(t *testing.T) {
	proposal := opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionScaleDeployment,
		Parameters: params(t, map[string]any{"replicas": float64(4)}),
	}
	digest, _ := BuildPlanDigest(DigestInput{
		IncidentUID: types.UID("uid-1"), Target: testIncident().Spec.TargetRef,
		TargetResourceVersion: "rv-1", Action: proposal.Action,
		Parameters: map[string]any{"replicas": float64(4)},
		PolicyUID:  types.UID("pol-uid-1"), PolicyGeneration: 3,
	})
	proposal.PlanDigest = digest
	approval := &opsv1alpha1.RemediationApproval{
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{Name: "inc-1", UID: types.UID("uid-1"), ProposalRevision: 1},
			Decision:    opsv1alpha1.ApprovalApprove,
			PlanDigest:  digest,
			Actor:       "x",
			Reason:      "x",
			ExpiresAt:   metav1.NewTime(time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)), // 已过期
		},
	}
	in := baseInput(proposal)
	in.Approval = approval
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() || decision.Reasons[0].Code != ReasonApprovalExpired {
		t.Errorf("过期审批应拒绝: %+v", decision)
	}
}

// TestEvaluate_ApprovalTTLExceedsPolicy 验证防御性重校验：审批有效期不得超过
// Policy 的 ApprovalTTL，即使 ExpiresAt 在 now 之前仍视为合法。
func TestEvaluate_ApprovalTTLExceedsPolicy(t *testing.T) {
	proposal := opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionScaleDeployment,
		Parameters: params(t, map[string]any{"replicas": float64(4), "reason": "扩容"}),
	}
	digest, _ := BuildPlanDigest(DigestInput{
		IncidentUID: types.UID("uid-1"), Target: testIncident().Spec.TargetRef,
		TargetResourceVersion: "rv-1", Action: proposal.Action,
		Parameters: map[string]any{"replicas": float64(4), "reason": "扩容"},
		PolicyUID:  types.UID("pol-uid-1"), PolicyGeneration: 3,
	})
	proposal.PlanDigest = digest
	approval := &opsv1alpha1.RemediationApproval{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.NewTime(time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)),
		},
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{Name: "inc-1", UID: types.UID("uid-1"), ProposalRevision: 1},
			Decision:    opsv1alpha1.ApprovalApprove,
			PlanDigest:  digest,
			Actor:       "x",
			Reason:      "x",
			// 30 分钟后才过期，但 Policy ApprovalTTL 仅 1 分钟 → 应被重校验拒绝。
			ExpiresAt: metav1.NewTime(time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC)),
		},
	}
	in := baseInput(proposal)
	in.Policy.Spec.ApprovalTTL = &metav1.Duration{Duration: 1 * time.Minute}
	in.Now = time.Date(2026, 8, 1, 10, 5, 0, 0, time.UTC)
	in.Approval = approval
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() || decision.Reasons[0].Code != ReasonApprovalExpired {
		t.Errorf("超出策略 TTL 的审批应拒绝: %+v", decision)
	}
}

func TestEvaluate_ApprovalRejected(t *testing.T) {
	proposal := opsv1alpha1.ActionProposal{
		Revision:   1,
		Action:     opsv1alpha1.ActionScaleDeployment,
		Parameters: params(t, map[string]any{"replicas": float64(4)}),
	}
	digest, _ := BuildPlanDigest(DigestInput{
		IncidentUID: types.UID("uid-1"), Target: testIncident().Spec.TargetRef,
		TargetResourceVersion: "rv-1", Action: proposal.Action,
		Parameters: map[string]any{"replicas": float64(4)},
		PolicyUID:  types.UID("pol-uid-1"), PolicyGeneration: 3,
	})
	proposal.PlanDigest = digest
	approval := &opsv1alpha1.RemediationApproval{
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{Name: "inc-1", UID: types.UID("uid-1"), ProposalRevision: 1},
			Decision:    opsv1alpha1.ApprovalReject,
			PlanDigest:  digest,
			Actor:       "x",
			Reason:      "不批准",
			ExpiresAt:   metav1.NewTime(time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC)),
		},
	}
	in := baseInput(proposal)
	in.Approval = approval
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() {
		t.Errorf("拒绝审批应 Deny: %+v", decision)
	}
}

func TestEvaluate_ApprovalUIDMismatch(t *testing.T) {
	proposal := opsv1alpha1.ActionProposal{
		Revision: 1, Action: opsv1alpha1.ActionScaleDeployment,
		Parameters: params(t, map[string]any{"replicas": float64(4)}),
	}
	approval := &opsv1alpha1.RemediationApproval{
		Spec: opsv1alpha1.RemediationApprovalSpec{
			IncidentRef: opsv1alpha1.IncidentReference{Name: "inc-1", UID: types.UID("other-uid"), ProposalRevision: 1},
			Decision:    opsv1alpha1.ApprovalApprove,
			PlanDigest:  "sha256:" + strings.Repeat("a", 64),
			Actor:       "x",
			Reason:      "x",
			ExpiresAt:   metav1.NewTime(time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC)),
		},
	}
	in := baseInput(proposal)
	in.Approval = approval
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() || decision.Reasons[0].Code != ReasonApprovalMismatch {
		t.Errorf("UID 不匹配应拒绝: %+v", decision)
	}
}

func TestEvaluate_SuggestOnly(t *testing.T) {
	in := baseInput(restartProposal(t))
	setActionMode(in.Policy, opsv1alpha1.ActionRestartWorkload, opsv1alpha1.ModeSuggestOnly)
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if decision.Type != DecisionSuggestOnly {
		t.Errorf("SuggestOnly 模式应返回 SuggestOnly: %+v", decision)
	}
}

func TestEvaluate_AutoRejectsMediumRisk(t *testing.T) {
	// 策略错误地把 Scale 配 Auto(CRD 会拦,但评估器也要兜底)。
	proposal := opsv1alpha1.ActionProposal{
		Revision: 1, Action: opsv1alpha1.ActionScaleDeployment,
		Parameters: params(t, map[string]any{"replicas": float64(4)}),
	}
	in := baseInput(proposal)
	setActionMode(in.Policy, opsv1alpha1.ActionScaleDeployment, opsv1alpha1.ModeAuto)
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() || decision.Reasons[0].Code != ReasonHighRiskDenied {
		t.Errorf("中风险 Auto 应拒绝: %+v", decision)
	}
}

func TestBuildPlanDigest_StableAndSensitive(t *testing.T) {
	base := DigestInput{
		IncidentUID:           types.UID("uid-1"),
		Target:                testIncident().Spec.TargetRef,
		TargetResourceVersion: "rv-1",
		Action:                opsv1alpha1.ActionScaleDeployment,
		Parameters:            map[string]any{"replicas": float64(4), "reason": "扩容"},
		PolicyUID:             types.UID("pol-uid-1"),
		PolicyGeneration:      3,
	}
	d1, err := BuildPlanDigest(base)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	d2, _ := BuildPlanDigest(base)
	if d1 != d2 {
		t.Error("相同输入摘要应一致")
	}
	if !strings.HasPrefix(d1, "sha256:") || len(d1) != 7+64 {
		t.Errorf("摘要格式错误: %s", d1)
	}

	// 每个敏感字段变化都必须改变摘要。
	mutations := map[string]func(*DigestInput){
		"uid":       func(d *DigestInput) { d.IncidentUID = "uid-2" },
		"rv":        func(d *DigestInput) { d.TargetResourceVersion = "rv-2" },
		"action":    func(d *DigestInput) { d.Action = opsv1alpha1.ActionRestartWorkload },
		"params":    func(d *DigestInput) { d.Parameters = map[string]any{"replicas": float64(5)} },
		"policyUID": func(d *DigestInput) { d.PolicyUID = "pol-2" },
		"policyGen": func(d *DigestInput) { d.PolicyGeneration = 4 },
	}
	for name, mutate := range mutations {
		changed := base
		mutate(&changed)
		d3, _ := BuildPlanDigest(changed)
		if d3 == d1 {
			t.Errorf("%s 变化不应产生相同摘要", name)
		}
	}
}

func TestCanonicalJSON_MapOrderStable(t *testing.T) {
	a, _ := CanonicalJSON(map[string]any{"b": 1, "a": 2})
	b, _ := CanonicalJSON(map[string]any{"a": 2, "b": 1})
	if string(a) != string(b) {
		t.Errorf("map 顺序应不影响结果: %s vs %s", a, b)
	}
}

func TestVerifyPlanDigest(t *testing.T) {
	input := DigestInput{
		IncidentUID: types.UID("uid-1"), Target: testIncident().Spec.TargetRef,
		TargetResourceVersion: "rv-1", Action: opsv1alpha1.ActionRestartWorkload,
		Parameters: map[string]any{"reason": "x"}, PolicyUID: types.UID("p"), PolicyGeneration: 1,
	}
	digest, _ := BuildPlanDigest(input)
	if err := VerifyPlanDigest(digest, input); err != nil {
		t.Errorf("正确摘要应通过: %v", err)
	}
	if err := VerifyPlanDigest("sha256:"+strings.Repeat("0", 64), input); err == nil {
		t.Error("错误摘要应失败")
	}
}

func TestMatches_Selector(t *testing.T) {
	p := &opsv1alpha1.RemediationPolicy{
		Spec: opsv1alpha1.RemediationPolicySpec{
			TargetSelector: opsv1alpha1.TargetSelector{
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"aegisops.io/managed": "true"}},
				Kinds:             []string{"Deployment"},
			},
		},
	}
	ok, err := Matches(p, map[string]string{"aegisops.io/managed": "true"}, nil, "Deployment")
	if err != nil || !ok {
		t.Errorf("应匹配: %v %v", ok, err)
	}
	ok, _ = Matches(p, map[string]string{}, nil, "Deployment")
	if ok {
		t.Error("缺少标签不应匹配")
	}
	ok, _ = Matches(p, map[string]string{"aegisops.io/managed": "true"}, nil, "StatefulSet")
	if ok {
		t.Error("kind 不匹配不应命中")
	}
}

func TestSelectHighestPriority_Ambiguous(t *testing.T) {
	p1 := opsv1alpha1.RemediationPolicy{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Spec: opsv1alpha1.RemediationPolicySpec{Priority: 5}}
	p2 := opsv1alpha1.RemediationPolicy{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Spec: opsv1alpha1.RemediationPolicySpec{Priority: 5}}
	if _, err := SelectHighestPriority([]opsv1alpha1.RemediationPolicy{p1, p2}); err == nil {
		t.Error("并列优先级应报错（fail closed）")
	}
	p2.Spec.Priority = 6
	best, err := SelectHighestPriority([]opsv1alpha1.RemediationPolicy{p1, p2})
	if err != nil || best.Name != "b" {
		t.Errorf("应选最高优先级: %v %v", best, err)
	}
	best, _ = SelectHighestPriority(nil)
	if best != nil {
		t.Error("空列表应返回 nil")
	}
}
