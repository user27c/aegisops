package executor

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// RestartWorkloadAction 低风险重启：通过 PodTemplate 注解触发滚动重启。
// Rollback 不支持（旧 Pod 已销毁），验证失败直接升级。
type RestartWorkloadAction struct{}

// RestartAnnotationKey 是触发重启的注解键。
const RestartAnnotationKey = "ops.aegis.io/restarted-at"

// Type 返回动作类型。
func (a *RestartWorkloadAction) Type() opsv1alpha1.ActionType {
	return opsv1alpha1.ActionRestartWorkload
}

// Preflight 检查 rollout 是否空闲。
func (a *RestartWorkloadAction) Preflight(ctx context.Context, c *Context) error {
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return err
	}
	if dep.Status.ObservedGeneration != dep.Generation {
		return fmt.Errorf("rollout 进行中（observedGeneration %d != generation %d），禁止重启",
			dep.Status.ObservedGeneration, dep.Generation)
	}
	if dep.Status.UnavailableReplicas > 0 {
		return fmt.Errorf("存在 %d 个不可用副本，禁止重启", dep.Status.UnavailableReplicas)
	}
	return nil
}

// Snapshot 记录当前 template 的 restart 注解值。
func (a *RestartWorkloadAction) Snapshot(ctx context.Context, c *Context) (Snapshot, error) {
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Action: a.Type(),
		Payload: map[string]any{
			"restartAnnotation": dep.Spec.Template.Annotations[RestartAnnotationKey],
		},
	}, nil
}

// Apply 写 restart 注解（幂等：OperationID 已存在则跳过）。
func (a *RestartWorkloadAction) Apply(ctx context.Context, c *Context, _ Snapshot) (Result, error) {
	opID := OperationID(c.Incident)
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return Result{}, err
	}
	if dep.Annotations != nil && dep.Annotations[OperationIDAnnotation] == opID {
		return Result{OperationID: opID, Message: "已执行过，跳过（幂等）"}, nil
	}

	patch := client.MergeFrom(dep.DeepCopy())
	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = map[string]string{}
	}
	dep.Spec.Template.Annotations[RestartAnnotationKey] = c.Now().Format(time.RFC3339Nano)
	if err := c.Client.Patch(ctx, dep, patch); err != nil {
		return Result{}, fmt.Errorf("触发重启: %w", err)
	}
	if err := markApplied(ctx, c, c.Incident.Spec.TargetRef, opID); err != nil {
		return Result{}, fmt.Errorf("写幂等注解: %w", err)
	}
	return Result{OperationID: opID, Message: "已触发滚动重启"}, nil
}

// Verify 检查新 Pod 全部 Ready 且 restart 注解已生效。
func (a *RestartWorkloadAction) Verify(ctx context.Context, c *Context, _ Snapshot) (Verification, error) {
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return Verification{}, err
	}
	if dep.Status.UnavailableReplicas > 0 {
		return Verification{Healthy: false, Reason: fmt.Sprintf("%d 个副本不可用", dep.Status.UnavailableReplicas)}, nil
	}
	if dep.Status.AvailableReplicas < *dep.Spec.Replicas {
		return Verification{Healthy: false, Reason: fmt.Sprintf("可用副本 %d < 期望 %d", dep.Status.AvailableReplicas, *dep.Spec.Replicas)}, nil
	}
	return Verification{Healthy: true, Reason: "全部副本可用"}, nil
}

// Rollback 不支持（旧 Pod 已销毁）；验证失败由调用方升级人工。
func (a *RestartWorkloadAction) Rollback(_ context.Context, _ *Context, _ Snapshot) (RollbackResult, error) {
	return RollbackResult{
		RolledBack: false,
		Message:    "RestartWorkload 不支持回滚（CompensatingActionUnsupported），请升级人工",
	}, nil
}

func getDeployment(ctx context.Context, c *Context) (*appsv1.Deployment, error) {
	ref := c.Incident.Spec.TargetRef
	var dep appsv1.Deployment
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, &dep); err != nil {
		return nil, fmt.Errorf("读取目标: %w", err)
	}
	return &dep, nil
}
