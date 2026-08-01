package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// handleDetected：确认目标存在、建立 Finalizer；
// 若源已 resolved 则转 RecoveredWithoutAction；否则进入 CollectingEvidence。
func (r *IncidentReconciler) handleDetected(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (ctrl.Result, error) {
	now := r.Clock.Now()

	// 目标必须存在（fail closed）。
	if err := r.checkTargetExists(ctx, i); err != nil {
		SetCondition(i, "Ready", metav1.ConditionFalse, "TargetNotFound", err.Error())
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "目标不存在", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// 来源已 resolved 且未执行任何变更 → 无需动作。
	if i.Spec.SourceStatus == "resolved" {
		if err := Terminalize(i, opsv1alpha1.PhaseRecoveredNoAction, "告警已自动恢复", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if err := Transition(i, opsv1alpha1.PhaseCollectingEvidence, "EvidenceReady", "开始采集证据", now); err != nil {
		return ctrl.Result{}, err
	}
	SetCondition(i, "Ready", metav1.ConditionTrue, "Detected", "目标确认，进入证据采集")
	return ctrl.Result{}, nil
}

// handleCollectingEvidence：采集一次证据；hash 相同不重复保存；
// 诊断服务未启用时保持该阶段（M3 接入提交）。
func (r *IncidentReconciler) handleCollectingEvidence(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (ctrl.Result, error) {
	now := r.Clock.Now()

	// 来源已 resolved 且尚未执行任何变更 → 无需动作。
	if i.Spec.SourceStatus == "resolved" && !hasExecuted(i) {
		if err := Terminalize(i, opsv1alpha1.PhaseRecoveredNoAction, "告警已自动恢复", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if r.Collector == nil {
		return ctrl.Result{}, fmt.Errorf("evidence collector 未配置")
	}
	pack, err := r.Collector.Collect(ctx, i)
	if err != nil {
		SetCondition(i, "EvidenceReady", metav1.ConditionFalse, "CollectFailed", truncateMessage(err.Error()))
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "证据采集失败", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// hash 相同不重复保存（M3 起写入 PostgreSQL；当前写 Status 摘要）。
	if i.Status.Evidence == nil || i.Status.Evidence.Hash != pack.Hash {
		counts := map[string]int{}
		for _, item := range pack.Items {
			counts[string(item.Kind)]++
		}
		i.Status.Evidence = &opsv1alpha1.EvidenceSummary{
			ID:   string(pack.IncidentUID),
			Hash: pack.Hash,
			Window: opsv1alpha1.TimeWindow{
				Start: metav1.NewTime(pack.Window.Start),
				End:   metav1.NewTime(pack.Window.End),
			},
			Counts:     counts,
			Redactions: len(pack.Redactions),
		}
	}

	// 诊断服务未启用：保持在 CollectingEvidence，等待后续里程碑。
	if !r.DiagnosisEnabled {
		return ctrl.Result{RequeueAfter: r.evidenceInterval()}, nil
	}

	// M3：提交 Analysis 并转 Diagnosing。
	if err := Transition(i, opsv1alpha1.PhaseDiagnosing, "AnalysisSubmitted", "分析任务已提交", now); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// hasExecuted 判断是否已执行过修复动作。
func hasExecuted(i *opsv1alpha1.AIOpsIncident) bool {
	return i.Status.Execution != nil && i.Status.Execution.Reference != nil
}

// checkTargetExists 确认目标 Deployment 存在。
func (r *IncidentReconciler) checkTargetExists(ctx context.Context, i *opsv1alpha1.AIOpsIncident) error {
	var dep appsv1.Deployment
	err := r.Get(ctx, client.ObjectKey{Namespace: i.Spec.TargetRef.Namespace, Name: i.Spec.TargetRef.Name}, &dep)
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("目标 Deployment %s/%s 不存在", i.Spec.TargetRef.Namespace, i.Spec.TargetRef.Name)
	}
	return err
}

func (r *IncidentReconciler) evidenceInterval() time.Duration {
	if r.RequeueEvidenceInterval > 0 {
		return r.RequeueEvidenceInterval
	}
	return 30 * time.Second
}
