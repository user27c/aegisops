package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/analysisclient"
	"github.com/user27c/aegisops/internal/executor"
	"github.com/user27c/aegisops/internal/targetlock"
)

// 验证参数。
const (
	verifyInterval        = 15 * time.Second
	verifyRequiredSuccess = 2
)

// handleExecuting：执行动作（Preflight → Snapshot → Apply → Verifying）。
// OperationID 幂等：同一方案只 Apply 一次。
func (r *IncidentReconciler) handleExecuting(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (ctrl.Result, error) {
	ctx, span := r.childSpan(ctx, "executor.apply")
	defer span.End()
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

	// 目标修复锁：进入执行前必须持有；被其他 Incident 持有则保持阶段等待。
	if r.TargetLock != nil {
		lockResult, lockErr := r.ensureTargetLock(ctx, i)
		if lockErr != nil {
			return lockResult, lockErr
		}
		if i.IsTerminal() {
			return lockResult, nil
		}
		// 被锁阻挡(ErrTargetLocked → RequeueAfter=10s)：不继续执行。
		if lockResult.RequeueAfter > 0 {
			return lockResult, nil
		}
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
	prefCtx, prefSpan := r.childSpan(ctx, "executor.preflight")
	if err := action.Preflight(prefCtx, execCtx); err != nil {
		prefSpan.End()
		SetCondition(i, "ExecutionReady", metav1.ConditionFalse, "PreflightFailed", truncateMessage(err.Error()))
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "Preflight 失败: "+err.Error(), now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	prefSpan.End()

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

	// Snapshot（Apply 前必须保存执行前状态，否则无法回滚 → fail closed）。
	snapCtx, snapSpan := r.childSpan(ctx, "executor.snapshot")
	snap, err := action.Snapshot(snapCtx, execCtx)
	if err != nil {
		snapSpan.End()
		SetCondition(i, "ExecutionReady", metav1.ConditionFalse, "SnapshotFailed", truncateMessage(err.Error()))
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "快照失败: "+err.Error(), now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if r.Analysis == nil {
		snapSpan.End()
		SetCondition(i, "ExecutionReady", metav1.ConditionFalse, "SnapshotUnavailable", "诊断服务不可用，无法持久化快照")
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "快照服务不可用，禁止执行", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// 执行前必须写审计(Critical)：审计不可用 → fail-closed。
	if r.Audit != nil {
		if err := r.Audit.Critical(ctx, "audit|exec-start|"+opID, string(i.UID),
			"ExecutionStarted", "operator",
			map[string]any{"action": string(i.Status.Proposal.Action), "executionID": "exec-" + opID[:16]}); err != nil {
			snapSpan.End()
			SetCondition(i, "ExecutionReady", metav1.ConditionFalse, "AuditUnavailable", truncateMessage(err.Error()))
			if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "审计不可用，禁止执行", now); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}

	execID := fmt.Sprintf("exec-%s", opID[:16])
	snapRaw, err := json.Marshal(snap)
	if err != nil {
		snapSpan.End()
		return ctrl.Result{}, fmt.Errorf("序列化快照: %w", err)
	}
	snapRef, err := r.Analysis.PutSnapshot(snapCtx, "snapshot|"+opID, analysisclient.SnapshotRequest{
		IncidentUID: i.UID,
		ExecutionID: execID,
		ActionType:  string(i.Status.Proposal.Action),
		Snapshot:    snapRaw,
	})
	snapSpan.End()
	if err != nil {
		// 快照保存失败：不执行（无法保证可回滚）。
		SetCondition(i, "ExecutionReady", metav1.ConditionFalse, "SnapshotPersistFailed", truncateMessage(err.Error()))
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "快照保存失败，禁止执行: "+err.Error(), now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Apply。
	appCtx, appSpan := r.childSpan(ctx, "executor.apply_patch")
	result, err := action.Apply(appCtx, execCtx, snap)
	appSpan.End()
	if err != nil {
		SetCondition(i, "ExecutionReady", metav1.ConditionFalse, "ApplyFailed", truncateMessage(err.Error()))
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "执行失败: "+err.Error(), now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// 保留已获取的目标锁引用（Apply 成功才建立 Execution）。
	var targetLockRef *opsv1alpha1.TargetLockReference
	if i.Status.Execution != nil {
		targetLockRef = i.Status.Execution.TargetLock
	}
	i.Status.Execution = &opsv1alpha1.ExecutionStatus{
		Reference: &opsv1alpha1.ExecutionReference{
			ExecutionID: execID,
			OperationID: result.OperationID,
			SnapshotID:  snapRef.ID,
			StartedAt:   &metav1.Time{Time: now},
		},
		Attempts:   1,
		TargetLock: targetLockRef,
	}
	if r.Audit != nil {
		r.Audit.BestEffort(ctx, "audit|exec-done|"+opID, string(i.UID),
			"ExecutionCompleted", "operator",
			map[string]any{"action": string(i.Status.Proposal.Action), "message": result.Message})
	}
	if err := Transition(i, opsv1alpha1.PhaseVerifying, "VerificationStarted", result.Message, now); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: verifyInterval}, nil
}

// ensureTargetLock 在执行前获取/续约目标修复锁。
// 被其他 Incident 持有 → 保持阶段并 10s 重试;失锁 → fail-closed Escalated。
func (r *IncidentReconciler) ensureTargetLock(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (ctrl.Result, error) {
	now := r.Clock.Now()
	key := targetlock.KeyForIncident(i)
	holder := targetlock.HolderIdentity(i)
	// A contender that had to wait for another Incident must never replay the
	// same target mutation after the first holder reaches a terminal phase. A
	// fresh alert arriving after that terminal phase has no such condition and
	// can be evaluated normally; this only suppresses duplicate in-flight work.
	if condition := i.GetCondition("TargetLockReady"); condition != nil && condition.Reason == "TargetLockContended" {
		SetCondition(i, "ExecutionReady", metav1.ConditionFalse, "DuplicateTargetExecution",
			"同一目标已由另一个 Incident 执行，禁止重复写入")
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "目标已被并发 Incident 处理，禁止重复执行", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if i.Status.Execution != nil && i.Status.Execution.TargetLock != nil {
		tl := i.Status.Execution.TargetLock
		handle := targetlock.Handle{LeaseName: tl.LeaseName, HolderIdentity: tl.HolderIdentity}
		if _, err := r.TargetLock.Renew(ctx, key, handle); err != nil {
			SetCondition(i, "TargetLockReady", metav1.ConditionFalse, "TargetLockLost", truncateMessage(err.Error()))
			if termErr := Terminalize(i, opsv1alpha1.PhaseEscalated, "目标锁丢失: "+err.Error(), now); termErr != nil {
				return ctrl.Result{}, termErr
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil
	}

	handle, err := r.TargetLock.Acquire(ctx, key, holder)
	if err != nil {
		if errors.Is(err, targetlock.ErrTargetLocked) {
			SetCondition(i, "TargetLockReady", metav1.ConditionFalse, "TargetLockContended", truncateMessage(err.Error()))
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}
	if i.Status.Execution == nil {
		i.Status.Execution = &opsv1alpha1.ExecutionStatus{}
	}
	i.Status.Execution.TargetLock = &opsv1alpha1.TargetLockReference{
		LeaseName:      handle.LeaseName,
		HolderIdentity: handle.HolderIdentity,
		AcquiredAt:     &metav1.Time{Time: now},
		RenewTime:      &metav1.Time{Time: now},
	}
	SetCondition(i, "TargetLockReady", metav1.ConditionTrue, "TargetLockHeld", "已持有目标修复锁")
	return ctrl.Result{}, nil
}

// releaseTargetLockBestEffort 终态释放锁;失败仅记录(由过期机制兜底)。
func (r *IncidentReconciler) releaseTargetLockBestEffort(ctx context.Context, i *opsv1alpha1.AIOpsIncident) {
	if r.TargetLock == nil || i.Status.Execution == nil || i.Status.Execution.TargetLock == nil {
		return
	}
	tl := i.Status.Execution.TargetLock
	handle := targetlock.Handle{LeaseName: tl.LeaseName, HolderIdentity: tl.HolderIdentity}
	if err := r.TargetLock.Release(ctx, targetlock.KeyForIncident(i), handle); err != nil {
		r.logger(ctx).Error(err, "释放目标锁失败(将由过期机制兜底)", "incident", i.Name, "lease", tl.LeaseName)
	}
}

// renewTargetLock 在 Verifying/RollingBack 每次 Reconcile 同步续约(fencing check)。
func (r *IncidentReconciler) renewTargetLock(ctx context.Context, i *opsv1alpha1.AIOpsIncident) error {
	if r.TargetLock == nil || i.Status.Execution == nil || i.Status.Execution.TargetLock == nil {
		return nil
	}
	key := targetlock.KeyForIncident(i)
	handle := targetlock.Handle{
		LeaseName:      i.Status.Execution.TargetLock.LeaseName,
		HolderIdentity: i.Status.Execution.TargetLock.HolderIdentity,
	}
	if _, err := r.TargetLock.Renew(ctx, key, handle); err != nil {
		SetCondition(i, "TargetLockReady", metav1.ConditionFalse, "TargetLockLost", truncateMessage(err.Error()))
		return err
	}
	return nil
}

// handleVerifying：周期检查；连续成功 → Resolved；超时 → RollingBack。
func (r *IncidentReconciler) handleVerifying(ctx context.Context, i *opsv1alpha1.AIOpsIncident) (ctrl.Result, error) {
	now := r.Clock.Now()
	if err := r.renewTargetLock(ctx, i); err != nil {
		// 失锁：禁止继续验证目标，fail-closed 转 Escalated。
		if termErr := Terminalize(i, opsv1alpha1.PhaseEscalated, "目标锁丢失: "+err.Error(), now); termErr != nil {
			return ctrl.Result{}, termErr
		}
		return ctrl.Result{}, nil
	}
	if r.Verifier == nil {
		return ctrl.Result{}, fmt.Errorf("verifier 未配置")
	}

	// 验证窗口。
	window := r.verificationWindow(i)
	if i.Status.Verification != nil && i.Status.Verification.Deadline != nil && now.After(i.Status.Verification.Deadline.Time) {
		if r.Audit != nil {
			r.Audit.BestEffort(ctx, "audit|verify-timeout|"+string(i.UID), string(i.UID),
				"VerificationTimeout", "operator", nil)
		}
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
			if r.Audit != nil {
				r.Audit.BestEffort(ctx, "audit|resolved|"+string(i.UID), string(i.UID),
					"IncidentResolved", "operator",
					map[string]any{"category": i.Status.Diagnosis.Category})
			}
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
	if err := r.renewTargetLock(ctx, i); err != nil {
		if termErr := Terminalize(i, opsv1alpha1.PhaseEscalated, "目标锁丢失: "+err.Error(), now); termErr != nil {
			return ctrl.Result{}, termErr
		}
		return ctrl.Result{}, nil
	}
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
	// 从持久化快照恢复（执行前状态，Apply 时已保存）。
	if r.Analysis == nil || i.Status.Execution == nil || i.Status.Execution.Reference == nil ||
		i.Status.Execution.Reference.SnapshotID == "" {
		SetCondition(i, "RollbackReady", metav1.ConditionFalse, "NoSnapshot", "无执行前快照，无法回滚")
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, "回滚失败: 无执行前快照", now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	// API 按 execution_id 查询（SnapshotID 是数据库 UUID，仅审计用）。
	stored, err := r.Analysis.GetSnapshot(ctx, i.Status.Execution.Reference.ExecutionID)
	if err != nil {
		msg := "回滚失败: 快照读取失败: " + err.Error()
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, truncateMessage(msg), now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	var snap executor.Snapshot
	if err := json.Unmarshal(stored.Snapshot, &snap); err != nil {
		msg := "回滚失败: 快照解析失败: " + err.Error()
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, truncateMessage(msg), now); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	// 防错：快照动作必须与方案动作一致。
	if snap.Action != i.Status.Proposal.Action {
		msg := "回滚失败: 快照动作不匹配"
		if err := Terminalize(i, opsv1alpha1.PhaseEscalated, msg, now); err != nil {
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

	if r.Audit != nil {
		r.Audit.BestEffort(ctx, "audit|rolled-back|"+string(i.UID), string(i.UID),
			"IncidentRolledBack", "operator",
			map[string]any{"action": string(i.Status.Proposal.Action), "message": rollback.Message})
	}
	SetCondition(i, "RollbackReady", metav1.ConditionTrue, "RolledBack", rollback.Message)
	if err := Terminalize(i, opsv1alpha1.PhaseRolledBack, "已回滚", now); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// verificationWindow 返回策略判定时冻结的验证窗口；旧 Incident 或未配置时回退 2 分钟。
func (r *IncidentReconciler) verificationWindow(i *opsv1alpha1.AIOpsIncident) time.Duration {
	if i.Status.PolicyDecision != nil && i.Status.PolicyDecision.VerificationWindow != nil {
		if window := i.Status.PolicyDecision.VerificationWindow.Duration; window > 0 {
			return window
		}
	}
	return 2 * time.Minute
}

// logger 返回带 incident 上下文的日志器。
func (r *IncidentReconciler) logger(ctx context.Context) logr.Logger {
	return logr.FromContextOrDiscard(ctx)
}
