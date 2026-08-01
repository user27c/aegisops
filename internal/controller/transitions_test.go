package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// allPhases 是全部非终态 + 终态阶段。
var allPhases = []opsv1alpha1.IncidentPhase{
	opsv1alpha1.PhaseDetected,
	opsv1alpha1.PhaseCollectingEvidence,
	opsv1alpha1.PhaseDiagnosing,
	opsv1alpha1.PhasePolicyChecking,
	opsv1alpha1.PhaseAwaitingApproval,
	opsv1alpha1.PhaseExecuting,
	opsv1alpha1.PhaseVerifying,
	opsv1alpha1.PhaseRollingBack,
	opsv1alpha1.PhaseResolved,
	opsv1alpha1.PhaseRolledBack,
	opsv1alpha1.PhaseEscalated,
	opsv1alpha1.PhaseRecoveredNoAction,
}

func TestValidateTransition_Allowed(t *testing.T) {
	allowed := []struct{ from, to opsv1alpha1.IncidentPhase }{
		{opsv1alpha1.PhaseDetected, opsv1alpha1.PhaseCollectingEvidence},
		{opsv1alpha1.PhaseDetected, opsv1alpha1.PhaseRecoveredNoAction},
		{opsv1alpha1.PhaseCollectingEvidence, opsv1alpha1.PhaseDiagnosing},
		{opsv1alpha1.PhaseCollectingEvidence, opsv1alpha1.PhaseEscalated},
		{opsv1alpha1.PhaseDiagnosing, opsv1alpha1.PhasePolicyChecking},
		{opsv1alpha1.PhaseDiagnosing, opsv1alpha1.PhaseEscalated},
		{opsv1alpha1.PhasePolicyChecking, opsv1alpha1.PhaseAwaitingApproval},
		{opsv1alpha1.PhasePolicyChecking, opsv1alpha1.PhaseExecuting},
		{opsv1alpha1.PhasePolicyChecking, opsv1alpha1.PhaseEscalated},
		{opsv1alpha1.PhaseAwaitingApproval, opsv1alpha1.PhaseExecuting},
		{opsv1alpha1.PhaseAwaitingApproval, opsv1alpha1.PhaseEscalated},
		{opsv1alpha1.PhaseExecuting, opsv1alpha1.PhaseVerifying},
		{opsv1alpha1.PhaseExecuting, opsv1alpha1.PhaseEscalated},
		{opsv1alpha1.PhaseVerifying, opsv1alpha1.PhaseResolved},
		{opsv1alpha1.PhaseVerifying, opsv1alpha1.PhaseRollingBack},
		{opsv1alpha1.PhaseVerifying, opsv1alpha1.PhaseEscalated},
		{opsv1alpha1.PhaseRollingBack, opsv1alpha1.PhaseRolledBack},
		{opsv1alpha1.PhaseRollingBack, opsv1alpha1.PhaseEscalated},
	}
	for _, tr := range allowed {
		if err := ValidateTransition(tr.from, tr.to); err != nil {
			t.Errorf("允许的转移 %q→%q 被拒绝: %v", tr.from, tr.to, err)
		}
	}
}

func TestValidateTransition_Disallowed(t *testing.T) {
	// 逐对验证：不在 allowedTransitions 中的组合必须报错。
	for _, from := range allPhases {
		for _, to := range allPhases {
			if from == to {
				continue
			}
			// 允许组合的查询表（与 allowedTransitions 一致）。
			if isAllowedPair(from, to) {
				continue
			}
			if err := ValidateTransition(from, to); err == nil {
				t.Errorf("不允许的转移 %q→%q 被放行", from, to)
			}
		}
	}
}

func TestValidateTransition_UnknownPhase(t *testing.T) {
	if err := ValidateTransition("NotAPhase", opsv1alpha1.PhaseDetected); err == nil {
		t.Error("未知起始阶段应报错")
	}
}

func TestTransition_WritesState(t *testing.T) {
	i := newTestIncident(opsv1alpha1.PhaseDetected)
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	if err := Transition(i, opsv1alpha1.PhaseCollectingEvidence, "EvidenceReady", "开始采集", now); err != nil {
		t.Fatalf("Transition 失败: %v", err)
	}
	if i.Status.Phase != opsv1alpha1.PhaseCollectingEvidence {
		t.Errorf("Phase 未更新: %s", i.Status.Phase)
	}
	if len(i.Status.Timeline) != 1 || i.Status.Timeline[0].Type != "PhaseTransition" {
		t.Errorf("时间线未追加: %+v", i.Status.Timeline)
	}
	if c := i.GetCondition("EvidenceReady"); c == nil || c.Status != metav1.ConditionTrue {
		t.Error("条件未设置")
	}
}

func TestTransition_InvalidIncrementsCounter(t *testing.T) {
	called := false
	SetInvalidTransitionCounter(func(from, to opsv1alpha1.IncidentPhase) {
		called = true
		if from != opsv1alpha1.PhaseDetected || to != opsv1alpha1.PhaseResolved {
			t.Errorf("计数器参数错误: %s→%s", from, to)
		}
	})
	defer SetInvalidTransitionCounter(nil)

	i := newTestIncident(opsv1alpha1.PhaseDetected)
	if err := Transition(i, opsv1alpha1.PhaseResolved, "", "", time.Now()); err == nil {
		t.Error("Detected→Resolved 应被拒绝")
	}
	if !called {
		t.Error("无效跳转计数器未被调用")
	}
}

func TestTerminalize(t *testing.T) {
	now := time.Now()
	i := newTestIncident(opsv1alpha1.PhaseVerifying)
	if err := Terminalize(i, opsv1alpha1.PhaseResolved, "已恢复", now); err != nil {
		t.Fatalf("Terminalize 失败: %v", err)
	}
	if i.Status.Phase != opsv1alpha1.PhaseResolved {
		t.Errorf("终态未写入: %s", i.Status.Phase)
	}

	// 已终态再次 Terminalize 报错。
	if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "", now); err == nil {
		t.Error("已终态不应再 Terminalize")
	}

	// 非终态参数报错。
	i2 := newTestIncident(opsv1alpha1.PhaseDetected)
	if err := Terminalize(i2, opsv1alpha1.PhaseExecuting, "", now); err == nil {
		t.Error("Terminalize 非终态参数应报错")
	}
}

// isAllowedPair 与 allowedTransitions 保持一致，供对照测试使用。
func isAllowedPair(from, to opsv1alpha1.IncidentPhase) bool {
	for _, t := range allowedTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// newTestIncident 构造指定阶段的测试对象。
func newTestIncident(phase opsv1alpha1.IncidentPhase) *opsv1alpha1.AIOpsIncident {
	return &opsv1alpha1.AIOpsIncident{
		Status: opsv1alpha1.AIOpsIncidentStatus{Phase: phase},
	}
}
