package executor

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// RestoreConfigMapAction 中风险 ConfigMap 恢复。
// 只处理 ConfigMap data/binaryData；不修改 Secret。
type RestoreConfigMapAction struct{}

// Type 返回动作类型。
func (a *RestoreConfigMapAction) Type() opsv1alpha1.ActionType {
	return opsv1alpha1.ActionRestoreConfigMap
}

// Preflight 检查备份 ConfigMap 存在且 immutable。
func (a *RestoreConfigMapAction) Preflight(ctx context.Context, c *Context) error {
	params, err := proposalParams(c.Proposal)
	if err != nil {
		return err
	}
	target := fmt.Sprint(params["targetConfigMap"])
	backup := fmt.Sprint(params["backupConfigMap"])
	if target == "" || backup == "" || target == backup {
		return fmt.Errorf("targetConfigMap 与 backupConfigMap 必填且不能同名")
	}

	var backupCM corev1.ConfigMap
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: c.Incident.Namespace, Name: backup}, &backupCM); err != nil {
		return fmt.Errorf("备份 ConfigMap %s 不存在: %w", backup, err)
	}
	if backupCM.Immutable == nil || !*backupCM.Immutable {
		return fmt.Errorf("备份 ConfigMap %s 必须 immutable", backup)
	}
	return nil
}

// Snapshot 记录目标 ConfigMap 当前数据。
func (a *RestoreConfigMapAction) Snapshot(ctx context.Context, c *Context) (Snapshot, error) {
	params, err := proposalParams(c.Proposal)
	if err != nil {
		return Snapshot{}, err
	}
	target := fmt.Sprint(params["targetConfigMap"])
	var cm corev1.ConfigMap
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: c.Incident.Namespace, Name: target}, &cm); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Action: a.Type(),
		Payload: map[string]any{
			"targetConfigMap": target,
			"data":            cm.Data,
			"binaryData":      cm.BinaryData,
		},
	}, nil
}

// Apply 用备份数据覆盖目标（幂等）。
func (a *RestoreConfigMapAction) Apply(ctx context.Context, c *Context, _ Snapshot) (Result, error) {
	opID := OperationID(c.Incident)
	params, err := proposalParams(c.Proposal)
	if err != nil {
		return Result{}, err
	}
	target := fmt.Sprint(params["targetConfigMap"])
	backup := fmt.Sprint(params["backupConfigMap"])

	var targetCM corev1.ConfigMap
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: c.Incident.Namespace, Name: target}, &targetCM); err != nil {
		return Result{}, err
	}
	if targetCM.Annotations != nil && targetCM.Annotations[OperationIDAnnotation] == opID {
		return Result{OperationID: opID, Message: "已执行过，跳过（幂等）"}, nil
	}

	var backupCM corev1.ConfigMap
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: c.Incident.Namespace, Name: backup}, &backupCM); err != nil {
		return Result{}, err
	}

	patch := client.MergeFrom(targetCM.DeepCopy())
	targetCM.Data = backupCM.Data
	targetCM.BinaryData = backupCM.BinaryData
	if targetCM.Annotations == nil {
		targetCM.Annotations = map[string]string{}
	}
	targetCM.Annotations[OperationIDAnnotation] = opID
	if err := c.Client.Patch(ctx, &targetCM, patch); err != nil {
		return Result{}, fmt.Errorf("恢复 ConfigMap: %w", err)
	}
	return Result{OperationID: opID, Message: fmt.Sprintf("已从 %s 恢复 %s", backup, target)}, nil
}

// Verify 检查目标数据与备份一致。
func (a *RestoreConfigMapAction) Verify(ctx context.Context, c *Context, _ Snapshot) (Verification, error) {
	params, err := proposalParams(c.Proposal)
	if err != nil {
		return Verification{}, err
	}
	target := fmt.Sprint(params["targetConfigMap"])
	backup := fmt.Sprint(params["backupConfigMap"])

	var targetCM, backupCM corev1.ConfigMap
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: c.Incident.Namespace, Name: target}, &targetCM); err != nil {
		return Verification{}, err
	}
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: c.Incident.Namespace, Name: backup}, &backupCM); err != nil {
		return Verification{}, err
	}
	if !configMapDataEqual(targetCM.Data, backupCM.Data) || !configMapBinaryEqual(targetCM.BinaryData, backupCM.BinaryData) {
		return Verification{Healthy: false, Reason: "目标数据与备份不一致"}, nil
	}
	return Verification{Healthy: true, Reason: "数据已与备份一致"}, nil
}

// Rollback 恢复原数据。
func (a *RestoreConfigMapAction) Rollback(ctx context.Context, c *Context, snap Snapshot) (RollbackResult, error) {
	target, _ := snap.Payload["targetConfigMap"].(string)
	data, _ := snap.Payload["data"].(map[string]string)
	binary, _ := snap.Payload["binaryData"].(map[string][]byte)

	var cm corev1.ConfigMap
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: c.Incident.Namespace, Name: target}, &cm); err != nil {
		return RollbackResult{}, err
	}
	patch := client.MergeFrom(cm.DeepCopy())
	cm.Data = data
	cm.BinaryData = binary
	if err := c.Client.Patch(ctx, &cm, patch); err != nil {
		return RollbackResult{RolledBack: false, Message: err.Error()}, err //nolint:nilerr -- 结果由 RollbackResult 表达
	}
	return RollbackResult{RolledBack: true, Message: "已恢复原 ConfigMap 数据"}, nil
}

func configMapDataEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func configMapBinaryEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if string(b[k]) != string(v) {
			return false
		}
	}
	return true
}

// 以下辅助函数供 rollback.go 使用。
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func jsonUnmarshalBytes(raw []byte, out any) error {
	return json.Unmarshal(raw, out)
}

func metav1IsControlledBy(obj metav1.Object, owner metav1.Object) bool {
	return metav1.IsControlledBy(obj, owner)
}
