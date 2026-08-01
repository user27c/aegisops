// Package controller 实现 AIOpsIncident 状态机编排与 Approval 校验。
package controller

import (
	"context"
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// maxStatusMessageBytes 是 Status 中 message 的最大字节数（1 KiB）。
const maxStatusMessageBytes = 1024

// PatchStatus 用 MergeFrom 补丁写 Status；无变化时跳过。
func PatchStatus(ctx context.Context, c client.StatusClient, before, after *opsv1alpha1.AIOpsIncident) error {
	if reflect.DeepEqual(before.Status, after.Status) {
		return nil
	}
	return c.Status().Patch(ctx, after, client.MergeFrom(before))
}

// SetCondition 设置或更新条件。状态未变化时保留 LastTransitionTime。
// message 截断到 1 KiB，防止把原始日志/Prompt 写进 Status。
func SetCondition(i *opsv1alpha1.AIOpsIncident, typ string, status metav1.ConditionStatus, reason, msg string) {
	msg = truncateMessage(msg)
	cond := metav1.Condition{
		Type:               typ,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: i.Generation,
	}
	if existing := i.GetCondition(typ); existing != nil && existing.Status == status {
		cond.LastTransitionTime = existing.LastTransitionTime
	}
	i.SetCondition(cond)
}

// ClearPhaseEphemeralStatus 进入下一阶段时清理只属于上一阶段的临时数据。
// 保留跨阶段需要引用的字段（Evidence/Analysis/Diagnosis/Proposal/PolicyDecision 等）。
func ClearPhaseEphemeralStatus(i *opsv1alpha1.AIOpsIncident, next opsv1alpha1.IncidentPhase) {
	switch next {
	case opsv1alpha1.PhaseVerifying:
		// 审批已完成、即将执行：清理过期审批引用与执行错误细节。
		i.Status.Approval = nil
		if i.Status.Execution != nil {
			i.Status.Execution.LastError = ""
		}
	case opsv1alpha1.PhaseRollingBack:
		// 验证明细不再需要。
		if i.Status.Verification != nil {
			i.Status.Verification.Checks = nil
		}
	case opsv1alpha1.PhaseRecoveredNoAction, opsv1alpha1.PhaseResolved, opsv1alpha1.PhaseRolledBack, opsv1alpha1.PhaseEscalated:
		// 终态保留完整摘要供审计展示；只清理错误细节。
		if i.Status.Execution != nil {
			i.Status.Execution.LastError = ""
		}
	}
}

// truncateMessage 按字节截断并保证 UTF-8 完整。
func truncateMessage(msg string) string {
	if len(msg) <= maxStatusMessageBytes {
		return msg
	}
	cut := msg[:maxStatusMessageBytes]
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	return cut
}
