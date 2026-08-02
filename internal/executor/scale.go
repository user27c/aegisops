package executor

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// ScaleDeploymentAction 中风险扩缩容。
type ScaleDeploymentAction struct{}

// Type 返回动作类型。
func (a *ScaleDeploymentAction) Type() opsv1alpha1.ActionType {
	return opsv1alpha1.ActionScaleDeployment
}

// Preflight 检查无 HPA 管理。
func (a *ScaleDeploymentAction) Preflight(ctx context.Context, c *Context) error {
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return err
	}
	// 检查 HPA 是否管理该 Deployment。
	var hpaList unstructured.UnstructuredList
	hpaList.SetGroupVersionKind(schema.GroupVersionKind{Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler"})
	if err := c.Client.List(ctx, &hpaList, client.InNamespace(c.Incident.Namespace)); err == nil {
		for _, hpa := range hpaList.Items {
			target := hpa.Object["spec"].(map[string]any)["scaleTargetRef"].(map[string]any)
			if name, _ := target["name"].(string); name == dep.Name {
				return fmt.Errorf("目标受 HPA %s 管理，禁止直接改副本数", hpa.GetName())
			}
		}
	}
	return nil
}

// Snapshot 记录当前副本数。
func (a *ScaleDeploymentAction) Snapshot(ctx context.Context, c *Context) (Snapshot, error) {
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Action: a.Type(),
		Payload: map[string]any{
			"replicas": derefInt32(dep.Spec.Replicas),
		},
	}, nil
}

// Apply 修改副本数（幂等）。
func (a *ScaleDeploymentAction) Apply(ctx context.Context, c *Context, _ Snapshot) (Result, error) {
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
	replicasVal, ok := params["replicas"].(float64)
	if !ok {
		return Result{}, fmt.Errorf("replicas 参数缺失或非法")
	}
	target := int32(replicasVal)

	patch := client.MergeFrom(dep.DeepCopy())
	dep.Spec.Replicas = &target
	if err := c.Client.Patch(ctx, dep, patch); err != nil {
		return Result{}, fmt.Errorf("调整副本数: %w", err)
	}
	if err := markApplied(ctx, c, c.Incident.Spec.TargetRef, opID); err != nil {
		return Result{}, err
	}
	return Result{OperationID: opID, Message: fmt.Sprintf("副本数调整为 %d", target)}, nil
}

// Verify 检查副本就绪。
func (a *ScaleDeploymentAction) Verify(ctx context.Context, c *Context, _ Snapshot) (Verification, error) {
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return Verification{}, err
	}
	expected := derefInt32(dep.Spec.Replicas)
	if dep.Status.AvailableReplicas < expected {
		return Verification{Healthy: false, Reason: fmt.Sprintf("可用副本 %d < 期望 %d", dep.Status.AvailableReplicas, expected)}, nil
	}
	return Verification{Healthy: true, Reason: "副本就绪"}, nil
}

// Rollback 恢复原副本数。
func (a *ScaleDeploymentAction) Rollback(ctx context.Context, c *Context, snap Snapshot) (RollbackResult, error) {
	original, ok := snap.Payload["replicas"].(int32)
	if !ok {
		// JSON 往返后可能是 float64。
		if f, ok2 := snap.Payload["replicas"].(float64); ok2 {
			original = int32(f)
		} else {
			return RollbackResult{RolledBack: false, Message: "快照缺少副本数"}, fmt.Errorf("快照缺少副本数")
		}
	}
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return RollbackResult{}, err
	}
	patch := client.MergeFrom(dep.DeepCopy())
	dep.Spec.Replicas = &original
	if err := c.Client.Patch(ctx, dep, patch); err != nil {
		return RollbackResult{RolledBack: false, Message: err.Error()}, err
	}
	return RollbackResult{RolledBack: true, Message: fmt.Sprintf("副本数恢复为 %d", original)}, nil
}

func derefInt32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// proposalParams 解析方案参数。
func proposalParams(p opsv1alpha1.ActionProposal) (map[string]any, error) {
	if len(p.Parameters.Raw) == 0 {
		return map[string]any{}, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(p.Parameters.Raw, &out); err != nil {
		return nil, fmt.Errorf("参数非法: %w", err)
	}
	return out, nil
}
