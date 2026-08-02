package controller

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// 状态机转移表。任何未列入的跳转都必须在 ValidateTransition 报错。
var allowedTransitions = map[opsv1alpha1.IncidentPhase][]opsv1alpha1.IncidentPhase{
	opsv1alpha1.PhaseDetected: {
		opsv1alpha1.PhaseCollectingEvidence,
		opsv1alpha1.PhaseRecoveredNoAction,
	},
	opsv1alpha1.PhaseCollectingEvidence: {
		opsv1alpha1.PhaseDiagnosing,
		opsv1alpha1.PhaseRecoveredNoAction,
		opsv1alpha1.PhaseEscalated,
	},
	opsv1alpha1.PhaseDiagnosing: {
		opsv1alpha1.PhasePolicyChecking,
		opsv1alpha1.PhaseRecoveredNoAction,
		opsv1alpha1.PhaseEscalated,
	},
	opsv1alpha1.PhasePolicyChecking: {
		opsv1alpha1.PhaseAwaitingApproval,
		opsv1alpha1.PhaseExecuting,
		opsv1alpha1.PhaseRecoveredNoAction,
		opsv1alpha1.PhaseEscalated,
	},
	opsv1alpha1.PhaseAwaitingApproval: {
		opsv1alpha1.PhaseExecuting,
		opsv1alpha1.PhaseEscalated,
		opsv1alpha1.PhaseRecoveredNoAction,
	},
	opsv1alpha1.PhaseExecuting: {
		opsv1alpha1.PhaseVerifying,
		opsv1alpha1.PhaseRollingBack,
		opsv1alpha1.PhaseEscalated,
	},
	opsv1alpha1.PhaseVerifying: {
		opsv1alpha1.PhaseResolved,
		opsv1alpha1.PhaseRollingBack,
		opsv1alpha1.PhaseEscalated,
	},
	opsv1alpha1.PhaseRollingBack: {
		opsv1alpha1.PhaseRolledBack,
		opsv1alpha1.PhaseEscalated,
	},
	// 终端阶段无出边。
	opsv1alpha1.PhaseResolved:          {},
	opsv1alpha1.PhaseRolledBack:        {},
	opsv1alpha1.PhaseEscalated:         {},
	opsv1alpha1.PhaseRecoveredNoAction: {},
}

// invalidTransitionTotal 由 observability 注入的计数器（避免包循环，用函数变量）。
var invalidTransitionTotal = func(opsv1alpha1.IncidentPhase, opsv1alpha1.IncidentPhase) {}

// SetInvalidTransitionCounter 注入无效跳转指标函数（main 中注册）。
func SetInvalidTransitionCounter(fn func(opsv1alpha1.IncidentPhase, opsv1alpha1.IncidentPhase)) {
	if fn != nil {
		invalidTransitionTotal = fn
	}
}

// ValidateTransition 校验 from→to 是否在转移表中。
// 空 Phase 等价于 Detected（Gateway 创建后由本控制器推进）。
func ValidateTransition(from, to opsv1alpha1.IncidentPhase) error {
	if from == "" {
		from = opsv1alpha1.PhaseDetected
	}
	targets, ok := allowedTransitions[from]
	if !ok {
		return fmt.Errorf("未知起始阶段 %q", from)
	}
	for _, t := range targets {
		if t == to {
			return nil
		}
	}
	return fmt.Errorf("不允许的转移 %q → %q", from, to)
}

// Transition 执行状态转移：校验、写 Phase、追加时间线、更新条件。
func Transition(i *opsv1alpha1.AIOpsIncident, to opsv1alpha1.IncidentPhase, reason, message string, now time.Time) error {
	if err := ValidateTransition(i.Status.Phase, to); err != nil {
		invalidTransitionTotal(i.Status.Phase, to)
		return err
	}
	i.Status.Phase = to
	i.AppendTimeline(opsv1alpha1.TimelineEntry{
		Time:    metav1.NewTime(now),
		Type:    "PhaseTransition",
		Reason:  reason,
		Message: message,
	})
	ClearPhaseEphemeralStatus(i, to)
	if reason != "" {
		i.SetCondition(metav1.Condition{
			Type:               reason,
			Status:             metav1.ConditionTrue,
			Reason:             "PhaseTransition",
			Message:            message,
			LastTransitionTime: metav1.NewTime(now),
			ObservedGeneration: i.Generation,
		})
	}
	return nil
}

// Terminalize 直接进入终态（允许从任意非终态转移）。
func Terminalize(i *opsv1alpha1.AIOpsIncident, to opsv1alpha1.IncidentPhase, reason string, now time.Time) error {
	if !to.IsTerminal() {
		return fmt.Errorf("Terminalize 只接受终态，收到 %q", to)
	}
	if i.IsTerminal() {
		return fmt.Errorf("已处于终态 %q", i.Status.Phase)
	}
	i.Status.Phase = to
	i.AppendTimeline(opsv1alpha1.TimelineEntry{
		Time:    metav1.NewTime(now),
		Type:    "PhaseTransition",
		Reason:  reason,
		Message: reason,
	})
	ClearPhaseEphemeralStatus(i, to)
	return nil
}
