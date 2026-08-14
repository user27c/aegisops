package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/analysisclient"
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
	ctx, span := r.childSpan(ctx, "evidence.collect")
	defer span.End()
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

	// 诊断服务未启用：保持在 CollectingEvidence。
	if !r.DiagnosisEnabled || r.Analysis == nil {
		return ctrl.Result{RequeueAfter: r.evidenceInterval()}, nil
	}

	// 提交 Analysis（幂等键 = incidentUID|evidenceHash|promptVersion）。
	subCtx, subSpan := r.childSpan(ctx, "diagnosis.submit")
	key := fmt.Sprintf("%s|%s|%s", i.UID, pack.Hash, PromptVersion)
	resp, err := r.Analysis.Submit(subCtx, key, analysisclient.SubmitRequest{
		Incident: analysisclient.IncidentDTO{
			UID:          string(i.UID),
			Namespace:    i.Namespace,
			Name:         i.Name,
			CategoryHint: alertCategoryHint(i),
			Severity:     i.Spec.Severity,
			Target:       dtoTarget(i.Spec.TargetRef),
		},
		Evidence:       pack,
		RequestedModel: "deepseek-chat",
		PromptVersion:  PromptVersion,
	})
	subSpan.End()
	if err != nil {
		// 提交失败（3 秒超时等）：保持本阶段延后重试，不阻塞 workqueue。
		SetCondition(i, "AnalysisSubmitted", metav1.ConditionFalse, "SubmitFailed", truncateMessage(err.Error()))
		return ctrl.Result{RequeueAfter: r.evidenceInterval()}, nil
	}

	i.Status.Analysis = &opsv1alpha1.AnalysisReference{
		AnalysisID:    resp.AnalysisID,
		EvidenceID:    resp.EvidenceID,
		PromptVersion: PromptVersion,
		SubmittedAt:   &metav1.Time{Time: now},
	}
	if err := Transition(i, opsv1alpha1.PhaseDiagnosing, "AnalysisSubmitted", "分析任务已提交", now); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// handleDiagnosing：只轮询；queued/processing Requeue 5 秒；
// failed 转 Escalated；succeeded 写 Diagnosis/Proposal 并转 PolicyChecking。
func (r *IncidentReconciler) handleDiagnosing(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (ctrl.Result, error) {
	ctx, span := r.childSpan(ctx, "diagnosis.poll")
	defer span.End()
	now := r.Clock.Now()
	if r.Analysis == nil || i.Status.Analysis == nil || i.Status.Analysis.AnalysisID == "" {
		return ctrl.Result{}, fmt.Errorf("缺少分析任务引用")
	}

	resp, err := r.Analysis.Get(ctx, i.Status.Analysis.AnalysisID)
	if err != nil {
		// 网络类错误(非 4xx)→ ErrTransient:递增 Attempts 指数退避,保持 Phase。
		if analysisclient.IsRetryable(err) {
			if i.Status.Execution == nil {
				i.Status.Execution = &opsv1alpha1.ExecutionStatus{}
			}
			i.Status.Execution.Attempts++
			return ctrl.Result{}, fmt.Errorf("%w: %v", ErrTransient, err)
		}
		SetCondition(i, "DiagnosisReady", metav1.ConditionFalse, "PollFailed", truncateMessage(err.Error()))
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	switch resp.Status {
	case analysisclient.StatusQueued, analysisclient.StatusProcessing:
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	case analysisclient.StatusFailed:
		SetCondition(i, "DiagnosisReady", metav1.ConditionFalse, "AnalysisFailed", resp.ErrorCode)
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "分析失败: "+resp.ErrorCode, now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	case analysisclient.StatusSucceeded:
		if resp.Result == nil {
			return ctrl.Result{}, fmt.Errorf("succeeded 但结果为空")
		}
		writeDiagnosis(i, resp.Result, now)
		if err := Transition(i, opsv1alpha1.PhasePolicyChecking, "DiagnosisReady", "诊断完成", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	default:
		return ctrl.Result{}, fmt.Errorf("未知任务状态 %q", resp.Status)
	}
}

// PromptVersion 是提交给诊断服务的 Prompt 版本。
const PromptVersion = "diagnosis-v1"

// writeDiagnosis 把诊断结果写入 Status。
func writeDiagnosis(i *opsv1alpha1.AIOpsIncident, r *analysisclient.DiagnosisResult, now time.Time) {
	i.Status.Diagnosis = &opsv1alpha1.DiagnosisSummary{
		Category:        r.Category,
		RootCause:       r.RootCause,
		Confidence:      r.Confidence,
		EvidenceIDs:     r.EvidenceIDs,
		RunbookRefs:     r.RunbookRefs,
		ReviewerVerdict: r.Reviewer.Verdict,
	}
	if r.Proposal != nil {
		i.Status.Proposal = &opsv1alpha1.ActionProposal{
			Revision:    1,
			Action:      opsv1alpha1.ActionType(r.Proposal.Action),
			Parameters:  proposalParams(r.Proposal.Parameters),
			GeneratedAt: metav1.NewTime(now),
		}
	}
}

// alertCategoryHint 从告警名推断分类提示。
func alertCategoryHint(i *opsv1alpha1.AIOpsIncident) string {
	hints := map[string]string{
		"ContainerOOMKilled":         "OOMKilled",
		"ContainerCrashLooping":      "CrashLoop",
		"ImagePullBackOff":           "ImagePullBackOff",
		"CheckoutHTTP500s":           "CheckoutFailure",
		"ProbeFailure":               "ProbeFailure",
		"ContainerCPUThrottlingHigh": "CPUThrottling",
		"DependencyTimeout":          "DependencyTimeout",
	}
	if h, ok := hints[i.Spec.AlertName]; ok {
		return h
	}
	return ""
}

// dtoTarget 构造诊断服务的目标 DTO。
func dtoTarget(ref opsv1alpha1.TargetReference) (out struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
}) {
	out.APIVersion = ref.APIVersion
	out.Kind = ref.Kind
	out.Name = ref.Name
	return out
}

// proposalParams 把参数 JSON 转为 apiextensions JSON。
func proposalParams(raw []byte) apiextensionsv1.JSON {
	return apiextensionsv1.JSON{Raw: raw}
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
