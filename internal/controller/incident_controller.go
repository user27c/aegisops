// Package controller 实现 AIOpsIncident 状态机编排。
//
// 边界：只编排状态；不直接拼 PromQL、不直接构造 HTTP、不直接 Patch Deployment。
// 禁止在 Reconcile 内出现超过 3 秒的外部请求或 time.Sleep。
package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/analysisclient"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/user27c/aegisops/internal/audit"
	"github.com/user27c/aegisops/internal/evidence"
	"github.com/user27c/aegisops/internal/executor"
	"github.com/user27c/aegisops/internal/observability"
	"github.com/user27c/aegisops/internal/targetlock"
	"github.com/user27c/aegisops/internal/verifier"
)

// FinalizerName 是 Incident 的 finalizer。
const FinalizerName = "ops.aegis.io/incident-finalizer"

// IncidentReconciler 驱动 AIOpsIncident 状态机。
type IncidentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Collector 采集多源证据（必需源失败时不得调用 LLM）。
	Collector evidence.Collector
	// Analysis 是诊断服务客户端（M3 起非 nil）。
	Analysis analysisclient.Client
	// Executor 是类型化动作执行器（M5 起非 nil）。
	Executor *executor.Registry
	// Verifier 是健康验证器（M5 起非 nil）。
	Verifier verifier.Checker
	// Audit 是审计写入器（M6 起非 nil；Critical 失败 fail-closed）。
	Audit *audit.Writer
	// TargetLock 是同目标修复锁（M9.1 起；nil 时跳过锁语义）。
	TargetLock targetlock.Manager
	// Clock 是时钟（测试注入）。
	Clock clock.Clock
	// Metrics 是 Prometheus 指标。
	Metrics *observability.Metrics
	// DiagnosisEnabled 标记诊断服务是否已配置（M3 起为 true）。
	DiagnosisEnabled bool
	// RequeueEvidenceInterval 是证据采集后等待诊断的间隔。
	RequeueEvidenceInterval time.Duration
	// RequeueStuckInterval 是未知阶段的重试间隔。
	RequeueStuckInterval time.Duration
}

// +kubebuilder:rbac:groups=ops.aegis.io,resources=aiopsincidents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ops.aegis.io,resources=aiopsincidents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// 说明:生产部署使用 Helm 的命名空间级 leader-election Role(仅 aegisops-system);
// 此处 ClusterRole 仅供 kustomize 开发部署。
// +kubebuilder:rbac:groups=apps,resources=deployments;replicasets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods;events;configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=patch;update,resourceNames=checkout-config
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile 处理一次 Incident 状态推进。
func (r *IncidentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// 观测:每次 Reconcile 创建 span(不保存证据内容)。
	ctx, span := observability.Tracer("aegisops-operator").Start(
		ctx, "incident.reconcile",
		trace.WithAttributes(
			attribute.String("incident.namespace", req.Namespace),
			attribute.String("incident.name", req.Name),
		),
	)
	defer span.End()

	logger := logr.FromContextOrDiscard(ctx).WithValues("incident", req.String())
	ctx = logr.NewContext(ctx, logger)

	if r.Clock == nil {
		r.Clock = clock.RealClock{}
	}

	incident := &opsv1alpha1.AIOpsIncident{}
	if err := r.Get(ctx, req.NamespacedName, incident); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 处理删除：无外部资源时直接移除 finalizer（M3 起清理 PG checkpoint）。
	if !incident.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, incident)
	}

	// 终端阶段不再处理（先释放目标锁）。
	if incident.IsTerminal() {
		r.releaseTargetLockBestEffort(ctx, incident)
		return ctrl.Result{}, nil
	}

	// 建立 finalizer（单独一轮，避免 before 快照版本过期导致 Status Patch 冲突）。
	if !containsString(incident.Finalizers, FinalizerName) {
		patch := client.MergeFrom(incident.DeepCopy())
		incident.Finalizers = append(incident.Finalizers, FinalizerName)
		if err := r.Patch(ctx, incident, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("建立 finalizer: %w", err)
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// 按 Phase 分派。
	before := incident.DeepCopy()
	result, err := r.dispatchPhase(ctx, incident)
	if err != nil {
		if r.Metrics != nil {
			r.Metrics.ReconcileErrors.WithLabelValues(string(incident.Status.Phase)).Inc()
		}
		if errors.Is(err, ErrTransient) {
			// 暂时性错误：保持当前 Phase，指数退避重试（不进入错误重试循环）。
			// 先持久化 Attempts（退避计数），否则每次崩溃/重启都会重置退避。
			if patchErr := PatchStatus(ctx, r.Client, before, incident); patchErr != nil {
				return ctrl.Result{}, fmt.Errorf("写 Status: %w", patchErr)
			}
			attempt := 0
			if incident.Status.Execution != nil {
				attempt = incident.Status.Execution.Attempts
			}
			return ctrl.Result{RequeueAfter: transientRequeueAfter(attempt)}, nil
		}
		return result, err
	}

	// 终态释放目标锁（best effort，失败由过期机制兜底）。
	if incident.IsTerminal() {
		r.releaseTargetLockBestEffort(ctx, incident)
	}

	// 观测状态转移(指标失败不影响状态机)。
	r.observePhaseTransition(before, incident)

	// 状态变更统一走 Status Patch（避免整对象 Update 冲突）。
	if err := PatchStatus(ctx, r.Client, before, incident); err != nil {
		return ctrl.Result{}, fmt.Errorf("写 Status: %w", err)
	}
	return result, nil
}

// SetupWithManager 注册控制器与事件过滤。
func (r *IncidentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opsv1alpha1.AIOpsIncident{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		WithEventFilter(incidentPredicate()).
		Complete(r)
}

// dispatchPhase 按当前 Phase 调用对应 handler。
func (r *IncidentReconciler) dispatchPhase(ctx context.Context, incident *opsv1alpha1.AIOpsIncident) (ctrl.Result, error) {
	switch incident.Status.Phase {
	case "", opsv1alpha1.PhaseDetected:
		return r.handleDetected(ctx, incident)
	case opsv1alpha1.PhaseCollectingEvidence:
		return r.handleCollectingEvidence(ctx, incident)
	case opsv1alpha1.PhaseDiagnosing:
		return r.handleDiagnosing(ctx, incident)
	case opsv1alpha1.PhasePolicyChecking:
		return r.handlePolicyChecking(ctx, incident)
	case opsv1alpha1.PhaseAwaitingApproval:
		return r.handleAwaitingApproval(ctx, incident)
	case opsv1alpha1.PhaseExecuting:
		return r.handleExecuting(ctx, incident)
	case opsv1alpha1.PhaseVerifying:
		return r.handleVerifying(ctx, incident)
	case opsv1alpha1.PhaseRollingBack:
		return r.handleRollingBack(ctx, incident)
	default:
		return ctrl.Result{RequeueAfter: r.stuckInterval()}, nil
	}
}

// observePhaseTransition 记录状态转移与阶段耗时。
func (r *IncidentReconciler) observePhaseTransition(before, after *opsv1alpha1.AIOpsIncident) {
	if r.Metrics == nil || before.Status.Phase == after.Status.Phase {
		return
	}
	now := r.Clock.Now()
	r.Metrics.PhaseTransitions.WithLabelValues(string(before.Status.Phase), string(after.Status.Phase)).Inc()
	if n := len(after.Status.Timeline); n >= 2 {
		start := after.Status.Timeline[n-2].Time.Time
		if !start.IsZero() {
			r.Metrics.PhaseDuration.WithLabelValues(string(before.Status.Phase)).
				Observe(now.Sub(start).Seconds())
		}
	}
}

// childSpan 创建子 span(evidence.collect/policy.evaluate/executor.apply 等)。
func (r *IncidentReconciler) childSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return observability.Tracer("aegisops-operator").Start(ctx, name)
}

// handleDeletion 处理删除：M2 无外部资源，直接移除 finalizer。
func (r *IncidentReconciler) handleDeletion(ctx context.Context, incident *opsv1alpha1.AIOpsIncident) (ctrl.Result, error) {
	if !containsString(incident.Finalizers, FinalizerName) {
		return ctrl.Result{}, nil
	}
	patch := client.MergeFrom(incident.DeepCopy())
	incident.Finalizers = removeString(incident.Finalizers, FinalizerName)
	if err := r.Patch(ctx, incident, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("移除 finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// incidentPredicate 过滤无关更新：只关心 Spec 变化（Generation）、
// SourceStatus 变化、删除时间戳或创建事件，忽略纯 Status 变化（防热循环）。
func incidentPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return true },
		DeleteFunc: func(event.DeleteEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldInc, okOld := e.ObjectOld.(*opsv1alpha1.AIOpsIncident)
			newInc, okNew := e.ObjectNew.(*opsv1alpha1.AIOpsIncident)
			if !okOld || !okNew {
				return true
			}
			if oldInc.Generation != newInc.Generation {
				return true
			}
			if oldInc.Spec.SourceStatus != newInc.Spec.SourceStatus {
				return true
			}
			// Phase 变化（状态机推进）必须触发；无关 Status 变化（如时间线追加）忽略。
			if oldInc.Status.Phase != newInc.Status.Phase {
				return true
			}
			if !oldInc.DeletionTimestamp.IsZero() || !newInc.DeletionTimestamp.IsZero() {
				return true
			}
			return false
		},
	}
}

func (r *IncidentReconciler) stuckInterval() time.Duration {
	if r.RequeueStuckInterval > 0 {
		return r.RequeueStuckInterval
	}
	return 30 * time.Second
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func removeString(list []string, s string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// 确保 corev1 引用存在（后续 RBAC marker 生成需要）。
var _ = corev1.Pod{}
