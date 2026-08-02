package executor

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// PatchResourceLimitAction 中风险资源调整。
type PatchResourceLimitAction struct{}

// Type 返回动作类型。
func (a *PatchResourceLimitAction) Type() opsv1alpha1.ActionType {
	return opsv1alpha1.ActionPatchResourceLimit
}

// Preflight 检查容器存在。
func (a *PatchResourceLimitAction) Preflight(ctx context.Context, c *Context) error {
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return err
	}
	params, err := proposalParams(c.Proposal)
	if err != nil {
		return err
	}
	container := fmt.Sprint(params["container"])
	for _, ctr := range dep.Spec.Template.Spec.Containers {
		if ctr.Name == container {
			return nil
		}
	}
	return fmt.Errorf("容器 %s 不存在", container)
}

// Snapshot 记录当前容器资源。
func (a *PatchResourceLimitAction) Snapshot(ctx context.Context, c *Context) (Snapshot, error) {
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return Snapshot{}, err
	}
	params, err := proposalParams(c.Proposal)
	if err != nil {
		return Snapshot{}, err
	}
	container := fmt.Sprint(params["container"])
	for _, ctr := range dep.Spec.Template.Spec.Containers {
		if ctr.Name == container {
			return Snapshot{
				Action: a.Type(),
				Payload: map[string]any{
					"container": container,
					"limits":    quantityMap(ctr.Resources.Limits),
					"requests":  quantityMap(ctr.Resources.Requests),
				},
			}, nil
		}
	}
	return Snapshot{}, fmt.Errorf("容器 %s 不存在", container)
}

// Apply 调整 limit（幂等）。
func (a *PatchResourceLimitAction) Apply(ctx context.Context, c *Context, _ Snapshot) (Result, error) {
	opID := OperationID(c.Incident)
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return Result{}, err
	}
	if dep.Annotations != nil && dep.Annotations[OperationIDAnnotation] == opID {
		return Result{OperationID: opID, Message: "已执行过，跳过（幂等）"}, nil
	}

	params, err := proposalParams(c.Proposal)
	if err != nil {
		return Result{}, err
	}
	container := fmt.Sprint(params["container"])
	containerIndex := -1
	for idx, ctr := range dep.Spec.Template.Spec.Containers {
		if ctr.Name == container {
			containerIndex = idx
			break
		}
	}
	if containerIndex < 0 {
		return Result{}, fmt.Errorf("容器 %s 不存在", container)
	}

	patch := client.MergeFrom(dep.DeepCopy())
	ctr := &dep.Spec.Template.Spec.Containers[containerIndex]
	if ctr.Resources.Limits == nil {
		ctr.Resources.Limits = corev1.ResourceList{}
	}
	if v, ok := params["memoryLimit"].(string); ok && v != "" {
		q, err := resource.ParseQuantity(v)
		if err != nil {
			return Result{}, fmt.Errorf("memoryLimit 非法: %w", err)
		}
		ctr.Resources.Limits[corev1.ResourceMemory] = q
	}
	if v, ok := params["cpuLimit"].(string); ok && v != "" {
		q, err := resource.ParseQuantity(v)
		if err != nil {
			return Result{}, fmt.Errorf("cpuLimit 非法: %w", err)
		}
		ctr.Resources.Limits[corev1.ResourceCPU] = q
	}
	if err := c.Client.Patch(ctx, dep, patch); err != nil {
		return Result{}, fmt.Errorf("调整资源: %w", err)
	}
	if err := markApplied(ctx, c, c.Incident.Spec.TargetRef, opID); err != nil {
		return Result{}, fmt.Errorf("写幂等注解: %w", err)
	}
	return Result{OperationID: opID, Message: fmt.Sprintf("已调整容器 %s 的 limit", container)}, nil
}

// Verify 检查新 Pod 全部就绪（资源变化触发滚动，等待可用）。
func (a *PatchResourceLimitAction) Verify(ctx context.Context, c *Context, _ Snapshot) (Verification, error) {
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return Verification{}, err
	}
	if dep.Status.ObservedGeneration != dep.Generation {
		return Verification{Healthy: false, Reason: "rollout 进行中"}, nil
	}
	if dep.Status.UnavailableReplicas > 0 {
		return Verification{Healthy: false, Reason: fmt.Sprintf("%d 个副本不可用", dep.Status.UnavailableReplicas)}, nil
	}
	return Verification{Healthy: true, Reason: "新配置生效"}, nil
}

// Rollback 恢复原资源。
func (a *PatchResourceLimitAction) Rollback(ctx context.Context, c *Context, snap Snapshot) (RollbackResult, error) {
	container, _ := snap.Payload["container"].(string)
	limits, _ := snap.Payload["limits"].(map[string]string)
	requests, _ := snap.Payload["requests"].(map[string]string)

	dep, err := getDeployment(ctx, c)
	if err != nil {
		return RollbackResult{}, err
	}
	containerIndex := -1
	for idx, ctr := range dep.Spec.Template.Spec.Containers {
		if ctr.Name == container {
			containerIndex = idx
			break
		}
	}
	if containerIndex < 0 {
		return RollbackResult{RolledBack: false, Message: "容器不存在"}, fmt.Errorf("容器不存在")
	}

	patch := client.MergeFrom(dep.DeepCopy())
	ctr := &dep.Spec.Template.Spec.Containers[containerIndex]
	ctr.Resources.Limits = stringQuantityMap(limits)
	ctr.Resources.Requests = stringQuantityMap(requests)
	if err := c.Client.Patch(ctx, dep, patch); err != nil {
		return RollbackResult{RolledBack: false, Message: err.Error()}, err //nolint:nilerr -- 结果由 RollbackResult 表达
	}
	return RollbackResult{RolledBack: true, Message: "已恢复原资源限制"}, nil
}

// quantityMap 把 ResourceList 转字符串 map（快照 JSON 化）。
func quantityMap(rl corev1.ResourceList) map[string]string {
	out := map[string]string{}
	for k, v := range rl {
		out[string(k)] = v.String()
	}
	return out
}

func stringQuantityMap(m map[string]string) corev1.ResourceList {
	out := corev1.ResourceList{}
	for k, v := range m {
		if q, err := resource.ParseQuantity(v); err == nil {
			out[corev1.ResourceName(k)] = q
		}
	}
	return out
}
