// Package targetlock 提供同一 Kubernetes 目标的 Incident 互斥修复锁。
//
// 语义：
//   - 一个目标（cluster/namespace/kind/name）同一时间最多一个 Incident 持有锁；
//   - Holder 是 Incident UID（不是 name），旧 holder 无法释放新 holder 的锁；
//   - 租约默认 60s，20s 内续约；过期后其他 Incident 可乐观接管；
//   - 执行路径（Snapshot/Apply/Rollback）前必须 AssertHeld（fencing check）。
package targetlock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// DefaultLeaseDuration 是默认租约时长。
const DefaultLeaseDuration = 60 * time.Second

// TargetKey 标识一个修复目标。
type TargetKey struct {
	Cluster   string
	Namespace string
	Kind      string
	Name      string
}

// Handle 是持有锁的句柄。
type Handle struct {
	LeaseName       string
	HolderIdentity  string
	ExpiresAt       time.Time
	ResourceVersion string
}

// Manager 管理目标锁。
type Manager interface {
	// Acquire 获取锁；被他人持有且未过期返回 ErrTargetLocked。
	Acquire(ctx context.Context, key TargetKey, holder string) (Handle, error)
	// Renew 续约；holder 变化或已过期返回 ErrTargetLockLost。
	Renew(ctx context.Context, key TargetKey, handle Handle) (Handle, error)
	// Release 释放锁；仅 holder 本人可释放（否则静默跳过）。
	Release(ctx context.Context, key TargetKey, handle Handle) error
	// AssertHeld 写入前 fencing check；失锁返回 ErrTargetLockLost。
	AssertHeld(ctx context.Context, key TargetKey, holder string) error
}

// 错误定义。
var (
	// ErrTargetLocked 目标被其他 Incident 持有（未过期）。
	ErrTargetLocked = fmt.Errorf("目标被其他 Incident 锁定")
	// ErrTargetLockLost 本 Incident 已失锁（holder 变化或过期）。
	ErrTargetLockLost = fmt.Errorf("目标锁已丢失")
)

// LeaseName 生成 Lease 名称：aegis-target-<sha256(cluster|ns|kind|name) 前 20 位>。
func LeaseName(key TargetKey) string {
	raw := fmt.Sprintf("%s|%s|%s|%s", key.Cluster, key.Namespace, key.Kind, key.Name)
	sum := sha256.Sum256([]byte(raw))
	return "aegis-target-" + hex.EncodeToString(sum[:])[:20]
}

// HolderIdentity 返回 Incident 的锁持有者标识（UID）。
func HolderIdentity(incident *opsv1alpha1.AIOpsIncident) string {
	return string(incident.UID)
}

// KeyForIncident 从 Incident 构造目标键。
func KeyForIncident(incident *opsv1alpha1.AIOpsIncident) TargetKey {
	return TargetKey{
		Cluster:   incident.Spec.Cluster,
		Namespace: incident.Spec.TargetRef.Namespace,
		Kind:      incident.Spec.TargetRef.Kind,
		Name:      incident.Spec.TargetRef.Name,
	}
}
