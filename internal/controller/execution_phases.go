package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/executor"
)

// 验证参数。
const (
	verifyInterval        = 15 * time.Second
	verifyRequiredSuccess = 2
)

// handleExecuting：执行动作（Preflight → Snapshot → Apply → Verifying）。
// OperationID 幂等：同一方案只 Apply 一次。
func (r *IncidentReconciler) handleExecuting(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (ctrl.Result, error) {
	now := r.Clock.Now()
	if i.Status.Proposal == nil {
		SetCondition(i, "ExecutionReady", metav1.ConditionFalse, "NoProposal", "方案为空")
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "方案为空", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if r.Executor == nil {
		return ctrl.Result{}, fmt.Errorf("executor 未配置")
	}

	action, err := r.Executor.Get(i.Status.Proposal.Action)
	if err != nil {
		SetCondition(i, "ExecutionReady", metav1.ConditionFalse, "ActionUnregistered", truncateMessage(err.Error()))
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "动作未注册: "+err.Error(), now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	execCtx := &executor.Context{
		Client:   r.Client,
		Incident: i,
		Proposal: *i.Status.Proposal,
		Clock:    r.Clock.Now,
		Logger:   r.logger(ctx),
	}

	// Preflight。
	if err := action.Preflight(ctx, execCtx); err != nil {
		SetCondition(i, "ExecutionReady", metav1.ConditionFalse, "PreflightFailed", truncateMessage(err.Error()))
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "Preflight 失败: "+err.Error(), now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// 幂等：检查是否已 Apply（执行引用存在且 OperationID 一致）。
	opID := executor.OperationID(i)
	if i.Status.Execution != nil && i.Status.Execution.Reference != nil &&
		i.Status.Execution.Reference.OperationID == opID {
		// 已执行过：直接转 Verifying（崩溃恢复路径）。
		if err := Transition(i, opsv1alpha1.PhaseVerifying, "VerificationStarted", "已执行，进入验证", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: verifyInterval}, nil
	}

	// Snapshot。
	snap, err := action.Snapshot(ctx, execCtx)
	if err != nil {
		SetCondition(i, "ExecutionReady", metav1.ConditionFalse, "SnapshotFailed", truncateMessage(err.Error()))
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "快照失败: "+err.Error(), now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Apply。
	result, err := action.Apply(ctx, execCtx, snap)
	if err != nil {
		SetCondition(i, "ExecutionReady", metav1.ConditionFalse, "ApplyFailed", truncateMessage(err.Error()))
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "执行失败: "+err.Error(), now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	i.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{
			ExecutionID: fmt.Sprintf("exec-%s", opID[:16]),
			OperationID: result.OperationID,
			StartedAt:   &metav1.Time{Time: now},
		},
		Attempts: 1,
	}
	if err := Transition(i, opsv1alpha1.PhaseVerifying, "VerificationStarted", result.Message, now); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: verifyInterval}, nil
}

// handleVerifying：周期检查；连续成功 → Resolved；超时 → RollingBack。
func (r *IncidentReconciler) handleVerifying(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (ctrl.Result, error) {
	now := r.Clock.Now()
	if r.Verifier == nil {
		return ctrl.Result{}, fmt.Errorf("verifier 未配置")
	}

	// 验证窗口。
	window := r.verificationWindow(i)
	if i.Status.Verification != nil && i.Status.Verification.Deadline != nil && now.After(i.Status.Verification.Deadline.Time) {
		SetCondition(i, "VerificationReady", metav1.ConditionFalse, "VerificationTimeout", "验证超时")
		if err := Transition(i, opsv1alpha1.PhaseRollingBack, "VerificationTimeout", "验证超时，开始回滚", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	verification, err := r.Verifier.Check(ctx, i, r.Executor, r.logger(ctx))
	if err != nil {
		SetCondition(i, "VerificationReady", metav1.ConditionFalse, "CheckFailed", truncateMessage(err.Error()))
		return ctrl.Result{RequeueAfter: verifyInterval}, nil
	}

	if i.Status.Verification == nil {
		i.Status.Verification = &opsv1alpha1.VerificationSummary{
			Deadline: &metav1.Time{Time: now.Add(window)},
		}
	}
	if verification.Healthy {
		i.Status.Verification.ConsecutiveSuccesses++
		i.Status.Verification.State = "Healthy"
		SetCondition(i, "VerificationReady", metav1.ConditionTrue, "Healthy", verification.Reason)
		if i.Status.Verification.ConsecutiveSuccesses >= verifyRequiredSuccess {
			if err := Terminalize(i, opsv1alpha1.PhaseResolved, "验证连续成功，事故恢复", now); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	} else {
		i.Status.Verification.ConsecutiveSuccesses = 0
		i.Status.Verification.State = "Unhealthy"
		SetCondition(i, "VerificationReady", metav1.ConditionFalse, "Unhealthy", verification.Reason)
	}
	return ctrl.Result{RequeueAfter: verifyInterval}, nil
}

// handleRollingBack：执行回滚；成功 → RolledBack；失败 → Escalated。
func (r *IncidentReconciler) handleRollingBack(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (ctrl.Result, error) {
	now := r.Clock.Now()
	if r.Executor == nil || i.Status.Proposal == nil {
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "回滚不可用", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	action, err := r.Executor.Get(i.Status.Proposal.Action)
	if err != nil {
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "回滚失败: 动作未注册", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	execCtx := &executor.Context{
		Client:   r.Client,
		Incident: i,
		Proposal: *i.Status.Proposal,
		Clock:    r.Clock.Now,
		Logger:   r.logger(ctx),
	}
	// 从快照恢复：MVP 用动作的 Snapshot 重建（执行前状态在 PG 快照，M6 接 audit/snapshot API）。
	snap, err := action.Snapshot(ctx, execCtx)
	if err != nil {
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "回滚失败: 快照不可用", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	rollback, err := action.Rollback(ctx, execCtx, snap)
	if err != nil || !rollback.RolledBack {
		var msg string
		if err != nil {
			msg = err.Error()
		} else {
			msg = rollback.Message
		}
		SetCondition(i, "RollbackReady", metav1.ConditionFalse, "RollbackFailed", truncateMessage(msg))
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "回滚失败: "+msg, now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	SetCondition(i, "RollbackReady", metav1.ConditionTrue, "RolledBack", rollback.Message)
	if err := Terminalize(i, opsv1alpha1.PhaseRolledBack, "已回滚", now); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// verificationWindow 返回验证窗口（默认 2 分钟）。
func (r *IncidentReconciler) verificationWindow(i *opsv1alpha1.AIOpsIncident) time.Duration {
	if i.Status.PolicyDecision != nil {
		// 策略约束在 Evaluation 时确定；MVP 用固定窗口。
		_ = i
	}
	return 2 * time.Minute
}

// logger 返回带 incident 上下文的日志器。
func (r *IncidentReconciler) logger(ctx context.Context) logr.Logger {
	return logr.FromContextOrDiscard(ctx)
}
