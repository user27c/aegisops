package policy

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
)

// RestartParams 是 RestartWorkload 参数。
type RestartParams struct {
	Reason string `json:"reason"`
}

// RestartConstraints 是 RestartWorkload 约束。
type RestartConstraints struct {
	// MaxRestartsPerWindow 是窗口内最大重启次数（0=不限制）。
	MaxRestartsPerWindow int
	// RequireReason 要求必须提供理由。
	RequireReason bool
}

// ValidateRestart 校验重启参数。
func ValidateRestart(params RestartParams, c RestartConstraints) error {
	if c.RequireReason && len(params.Reason) < 4 {
		return fmt.Errorf("restart 必须提供至少 4 个字符的理由")
	}
	if len(params.Reason) > 256 {
		return fmt.Errorf("restart 理由过长")
	}
	return nil
}

// ScaleParams 是 ScaleDeployment 参数。
type ScaleParams struct {
	Replicas int32  `json:"replicas"`
	Reason   string `json:"reason"`
}

// ScaleConstraints 是 ScaleDeployment 约束。
type ScaleConstraints struct {
	MaxReplicas     int32
	MaxReplicaDelta int32
	// RespectHPA 为 true 时拒绝直接改副本数（HPA 管理场景）。
	RespectHPA bool
}

// ValidateScale 校验扩缩容。
func ValidateScale(current int32, params ScaleParams, c ScaleConstraints) error {
	if params.Replicas <= 0 {
		return fmt.Errorf("replicas 必须大于 0")
	}
	if c.MaxReplicas > 0 && params.Replicas > c.MaxReplicas {
		return fmt.Errorf("replicas %d 超过上限 %d", params.Replicas, c.MaxReplicas)
	}
	delta := params.Replicas - current
	if delta < 0 {
		delta = -delta
	}
	if c.MaxReplicaDelta > 0 && delta > c.MaxReplicaDelta {
		return fmt.Errorf("副本变化 %d 超过单次上限 %d", delta, c.MaxReplicaDelta)
	}
	if c.RespectHPA {
		return fmt.Errorf("目标受 HPA 管理，禁止直接改副本数")
	}
	return nil
}

// ResourcePatchParams 是 PatchResourceLimit 参数。
type ResourcePatchParams struct {
	Container   string `json:"container"`
	MemoryLimit string `json:"memoryLimit,omitempty"`
	CPULimit    string `json:"cpuLimit,omitempty"`
}

// ResourceConstraints 是 PatchResourceLimit 约束。
type ResourceConstraints struct {
	MaxMemory          *resource.Quantity
	MaxCPU             *resource.Quantity
	MaxIncreasePercent int32
	// AllowLimitRemoval 禁止移除 limit（必须 false）。
	AllowLimitRemoval bool
}

// ValidateResourcePatch 校验资源调整。
func ValidateResourcePatch(current map[string]resource.Quantity, params ResourcePatchParams, c ResourceConstraints) error {
	if params.Container == "" {
		return fmt.Errorf("container 必填")
	}
	if params.MemoryLimit == "" && params.CPULimit == "" {
		return fmt.Errorf("至少提供一个资源 limit")
	}
	if c.AllowLimitRemoval {
		return fmt.Errorf("禁止移除资源 limit")
	}

	if params.MemoryLimit != "" {
		mem, err := resource.ParseQuantity(params.MemoryLimit)
		if err != nil {
			return fmt.Errorf("memoryLimit 非法: %w", err)
		}
		if mem.Sign() <= 0 {
			return fmt.Errorf("memoryLimit 必须为正")
		}
		if c.MaxMemory != nil && mem.Cmp(*c.MaxMemory) > 0 {
			return fmt.Errorf("memoryLimit %s 超过策略上限 %s", mem.String(), c.MaxMemory.String())
		}
		if cur, ok := current["memory"]; ok && cur.Sign() > 0 {
			if pct := increasePercent(cur, mem); c.MaxIncreasePercent > 0 && pct > c.MaxIncreasePercent {
				return fmt.Errorf("内存增幅 %d%% 超过上限 %d%%", pct, c.MaxIncreasePercent)
			}
		}
	}
	if params.CPULimit != "" {
		cpu, err := resource.ParseQuantity(params.CPULimit)
		if err != nil {
			return fmt.Errorf("cpuLimit 非法: %w", err)
		}
		if cpu.Sign() <= 0 {
			return fmt.Errorf("cpuLimit 必须为正")
		}
		if c.MaxCPU != nil && cpu.Cmp(*c.MaxCPU) > 0 {
			return fmt.Errorf("cpuLimit %s 超过策略上限 %s", cpu.String(), c.MaxCPU.String())
		}
	}
	return nil
}

func increasePercent(old, newQty resource.Quantity) int32 {
	if old.Sign() <= 0 {
		return 0
	}
	delta := newQty.DeepCopy()
	delta.Sub(old)
	pct := float64(delta.AsApproximateFloat64()) / float64(old.AsApproximateFloat64()) * 100
	return int32(pct)
}

// RollbackParams 是 RollbackDeployment 参数。
type RollbackParams struct {
	TargetRevision int64  `json:"targetRevision"`
	Reason         string `json:"reason"`
}

// RollbackConstraints 是 RollbackDeployment 约束。
type RollbackConstraints struct {
	MaxRevisionDistance int64
}

// ValidateRollback 校验回滚。
func ValidateRollback(currentRevision int64, params RollbackParams, c RollbackConstraints) error {
	if params.TargetRevision <= 0 {
		return fmt.Errorf("targetRevision 必须为正")
	}
	if params.TargetRevision >= currentRevision {
		return fmt.Errorf("targetRevision %d 必须小于当前 revision %d", params.TargetRevision, currentRevision)
	}
	if c.MaxRevisionDistance > 0 && currentRevision-params.TargetRevision > c.MaxRevisionDistance {
		return fmt.Errorf("回滚距离 %d 超过上限 %d", currentRevision-params.TargetRevision, c.MaxRevisionDistance)
	}
	return nil
}

// RestoreConfigMapParams 是 RestoreConfigMap 参数。
type RestoreConfigMapParams struct {
	TargetConfigMap string `json:"targetConfigMap"`
	BackupConfigMap string `json:"backupConfigMap"`
}

// ConfigMapConstraints 是 RestoreConfigMap 约束。
type ConfigMapConstraints struct {
	AllowedNames           []string
	RequireImmutableBackup bool
}

// ValidateConfigRestore 校验 ConfigMap 恢复。
func ValidateConfigRestore(params RestoreConfigMapParams, c ConfigMapConstraints) error {
	if params.TargetConfigMap == "" || params.BackupConfigMap == "" {
		return fmt.Errorf("targetConfigMap 与 backupConfigMap 必填")
	}
	if params.TargetConfigMap == params.BackupConfigMap {
		return fmt.Errorf("目标与备份不能同名")
	}
	if len(c.AllowedNames) > 0 {
		allowed := false
		for _, n := range c.AllowedNames {
			if n == params.TargetConfigMap {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("ConfigMap %s 不在白名单", params.TargetConfigMap)
		}
	}
	if c.RequireImmutableBackup {
		// 备份不可变与内容 hash 由调用方在读取 ConfigMap 后校验（本函数只做参数校验）。
		_ = c.RequireImmutableBackup
	}
	return nil
}
