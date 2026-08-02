// Package executor 是唯一允许修改工作负载的包。
//
// 每个动作必须实现 Preflight / Snapshot / Apply / Verify / Rollback。
// Apply 通过 OperationID 幂等：同一 Incident 同一方案只执行一次。
package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// OperationIDAnnotation 是 Apply 时写入目标 Deployment 的幂等注解。
const OperationIDAnnotation = "ops.aegis.io/operation-id"

// LastActionAtAnnotation 记录同一目标最近一次动作时间（冷却期用）。
const LastActionAtAnnotation = "ops.aegis.io/last-action-at"

// Context 是动作执行上下文。
type Context struct {
	Client   client.Client
	Incident *opsv1alpha1.AIOpsIncident
	Proposal opsv1alpha1.ActionProposal
	Clock    func() time.Time
	Logger   logr.Logger
}

// Now 返回当前时间。
func (c *Context) Now() time.Time {
	if c.Clock != nil {
		return c.Clock()
	}
	return time.Now()
}

// Snapshot 是执行前快照（回滚依据）。
type Snapshot struct {
	// Action 是动作类型。
	Action opsv1alpha1.ActionType
	// Payload 是动作特定的快照数据（JSON 可序列化）。
	Payload map[string]any
}

// Result 是 Apply 结果。
type Result struct {
	// OperationID 是幂等键。
	OperationID string
	// Message 是给用户的说明。
	Message string
}

// Verification 是单次验证结果。
type Verification struct {
	// Healthy 标记是否通过。
	Healthy bool
	// Reason 是失败原因。
	Reason string
}

// RollbackResult 是回滚结果。
type RollbackResult struct {
	// RolledBack 标记是否成功回滚。
	RolledBack bool
	// Message 是说明。
	Message string
}

// Action 是类型化修复动作接口。
type Action interface {
	// Type 返回动作类型。
	Type() opsv1alpha1.ActionType
	// Preflight 执行前检查（目标状态/约束）。
	Preflight(ctx context.Context, c *Context) error
	// Snapshot 保存执行前状态。
	Snapshot(ctx context.Context, c *Context) (Snapshot, error)
	// Apply 执行变更（幂等：OperationID 已存在则跳过）。
	Apply(ctx context.Context, c *Context, snap Snapshot) (Result, error)
	// Verify 做一次无副作用检查。
	Verify(ctx context.Context, c *Context, snap Snapshot) (Verification, error)
	// Rollback 恢复到快照状态。
	Rollback(ctx context.Context, c *Context, snap Snapshot) (RollbackResult, error)
}

// OperationID 计算幂等键：sha256(incidentUID|planDigest)。
func OperationID(i *opsv1alpha1.AIOpsIncident) string {
	digest := ""
	if i.Status.Proposal != nil {
		digest = i.Status.Proposal.PlanDigest
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s", i.UID, digest)))
	return hex.EncodeToString(sum[:])
}

// markApplied 写入 OperationID 与 last-action-at 注解。
func markApplied(ctx context.Context, c *Context, ref opsv1alpha1.TargetReference, operationID string) error {
	var dep appsv1.Deployment
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, &dep); err != nil {
		return err
	}
	patch := client.MergeFrom(dep.DeepCopy())
	if dep.Annotations == nil {
		dep.Annotations = map[string]string{}
	}
	dep.Annotations[OperationIDAnnotation] = operationID
	dep.Annotations[LastActionAtAnnotation] = c.Now().Format(time.RFC3339)
	return c.Client.Patch(ctx, &dep, patch)
}
