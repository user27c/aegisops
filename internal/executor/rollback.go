package executor

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// RollbackDeploymentAction 中风险回滚到历史 revision。
type RollbackDeploymentAction struct{}

// Type 返回动作类型。
func (a *RollbackDeploymentAction) Type() opsv1alpha1.ActionType {
	return opsv1alpha1.ActionRollbackDeployment
}

// Preflight 检查目标 revision 存在且为历史版本。
func (a *RollbackDeploymentAction) Preflight(ctx context.Context, c *Context) error {
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return err
	}
	params, err := proposalParams(c.Proposal)
	if err != nil {
		return err
	}
	target := int64(params["targetRevision"].(float64))
	if target <= 0 {
		return fmt.Errorf("targetRevision 必须为正")
	}
	revisions, err := listRevisions(ctx, c, dep)
	if err != nil {
		return err
	}
	for _, rev := range revisions {
		if rev == target {
			return nil
		}
	}
	return fmt.Errorf("目标 revision %d 不存在（现有: %v）", target, revisions)
}

// Snapshot 记录当前 template。
func (a *RollbackDeploymentAction) Snapshot(ctx context.Context, c *Context) (Snapshot, error) {
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return Snapshot{}, err
	}
	// 用 JSON 保留完整 template。
	raw, err := jsonMarshal(dep.Spec.Template)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Action: a.Type(),
		Payload: map[string]any{
			"template": string(raw),
		},
	}, nil
}

// Apply 通过 patch PodTemplate 到目标 revision 的 template 实现回滚。
func (a *RollbackDeploymentAction) Apply(ctx context.Context, c *Context, _ Snapshot) (Result, error) {
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
	target := int64(params["targetRevision"].(float64))

	// 从 ReplicaSet 找到目标 revision 的 template。
	targetTemplate, err := findRevisionTemplate(ctx, c, dep, target)
	if err != nil {
		return Result{}, err
	}

	patch := client.MergeFrom(dep.DeepCopy())
	dep.Spec.Template = *targetTemplate
	if err := c.Client.Patch(ctx, dep, patch); err != nil {
		return Result{}, fmt.Errorf("回滚到 revision %d: %w", target, err)
	}
	if err := markApplied(ctx, c, c.Incident.Spec.TargetRef, opID); err != nil {
		return Result{}, fmt.Errorf("写幂等注解: %w", err)
	}
	return Result{OperationID: opID, Message: fmt.Sprintf("已回滚到 revision %d", target)}, nil
}

// Verify 检查 rollout 完成且新版本 Available。
func (a *RollbackDeploymentAction) Verify(ctx context.Context, c *Context, _ Snapshot) (Verification, error) {
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
	return Verification{Healthy: true, Reason: "回滚目标已可用"}, nil
}

// Rollback 恢复原 template。
func (a *RollbackDeploymentAction) Rollback(ctx context.Context, c *Context, snap Snapshot) (RollbackResult, error) {
	templateRaw, ok := snap.Payload["template"].(string)
	if !ok || templateRaw == "" {
		return RollbackResult{RolledBack: false, Message: "快照缺少 template"}, fmt.Errorf("快照缺少 template")
	}
	dep, err := getDeployment(ctx, c)
	if err != nil {
		return RollbackResult{}, err
	}
	var original corev1.PodTemplateSpec
	if err := jsonUnmarshalBytes([]byte(templateRaw), &original); err != nil {
		return RollbackResult{RolledBack: false, Message: err.Error()}, err
	}
	patch := client.MergeFrom(dep.DeepCopy())
	dep.Spec.Template = original
	if err := c.Client.Patch(ctx, dep, patch); err != nil {
		return RollbackResult{RolledBack: false, Message: err.Error()}, err //nolint:nilerr -- 结果由 RollbackResult 表达
	}
	return RollbackResult{RolledBack: true, Message: "已恢复原 template"}, nil
}

// listRevisions 列出该 Deployment 控制的 ReplicaSet revision。
func listRevisions(ctx context.Context, c *Context, dep *appsv1.Deployment) ([]int64, error) {
	var rsList appsv1.ReplicaSetList
	if err := c.Client.List(ctx, &rsList, client.InNamespace(dep.Namespace)); err != nil {
		return nil, err
	}
	revisions := []int64{}
	for _, rs := range rsList.Items {
		if !metav1IsControlledBy(&rs, dep) {
			continue
		}
		if rev, err := strconv.ParseInt(rs.Annotations["deployment.kubernetes.io/revision"], 10, 64); err == nil {
			revisions = append(revisions, rev)
		}
	}
	sort.Slice(revisions, func(i, j int) bool { return revisions[i] < revisions[j] })
	return revisions, nil
}

// findRevisionTemplate 找到目标 revision 的 ReplicaSet template。
func findRevisionTemplate(ctx context.Context, c *Context, dep *appsv1.Deployment, target int64) (*corev1.PodTemplateSpec, error) {
	var rsList appsv1.ReplicaSetList
	if err := c.Client.List(ctx, &rsList, client.InNamespace(dep.Namespace)); err != nil {
		return nil, err
	}
	for _, rs := range rsList.Items {
		if !metav1IsControlledBy(&rs, dep) {
			continue
		}
		rev, err := strconv.ParseInt(rs.Annotations["deployment.kubernetes.io/revision"], 10, 64)
		if err != nil {
			continue
		}
		if rev == target {
			return &rs.Spec.Template, nil
		}
	}
	return nil, fmt.Errorf("目标 revision %d 不存在", target)
}
