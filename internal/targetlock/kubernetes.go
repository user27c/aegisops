package targetlock

import (
	"context"
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// KubernetesManager 基于 coordination.k8s.io/v1 Lease 实现 Manager。
type KubernetesManager struct {
	Client client.Client
	// LeaseDuration 租约时长（默认 60s）。
	LeaseDuration time.Duration
	// Clock 可注入时钟（测试用）。
	Now func() time.Time
}

// NewKubernetesManager 构造管理器。
func NewKubernetesManager(c client.Client) *KubernetesManager {
	return &KubernetesManager{Client: c, Now: time.Now}
}

func (m *KubernetesManager) leaseDuration() time.Duration {
	if m.LeaseDuration <= 0 {
		return DefaultLeaseDuration
	}
	return m.LeaseDuration
}

func (m *KubernetesManager) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}

func leaseKey(namespace, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: namespace, Name: name}
}

func (m *KubernetesManager) leaseSpec(_ TargetKey, holder string, renewTime time.Time) coordinationv1.LeaseSpec {
	leaseDuration := int32(m.leaseDuration().Seconds())
	return coordinationv1.LeaseSpec{
		HolderIdentity:       &holder,
		LeaseDurationSeconds: &leaseDuration,
		RenewTime:            &metav1.MicroTime{Time: renewTime},
	}
}

func (m *KubernetesManager) held(lease *coordinationv1.Lease, now time.Time) bool {
	if lease.Spec.HolderIdentity == nil {
		return false
	}
	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return true // 无续约信息视为仍持有（fail-closed）
	}
	expires := lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
	return now.Before(expires)
}

// Acquire 获取或重入锁。
func (m *KubernetesManager) Acquire(ctx context.Context, key TargetKey, holder string) (Handle, error) {
	name := LeaseName(key)
	now := m.now()

	lease := &coordinationv1.Lease{}
	err := m.Client.Get(ctx, leaseKey(key.Namespace, name), lease)
	if apierrors.IsNotFound(err) {
		// 创建：用 holder 命名 holderIdentity（防止半初始化 Lease 被误判）。
		createHolder := holder
		lease = &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: name},
			Spec:       m.leaseSpec(key, createHolder, now),
		}
		if err := m.Client.Create(ctx, lease); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return m.Acquire(ctx, key, holder) // 并发创建：重读重试
			}
			return Handle{}, fmt.Errorf("创建目标锁: %w", err)
		}
		return handleFrom(lease, m.leaseDuration(), now), nil
	}
	if err != nil {
		return Handle{}, fmt.Errorf("读取目标锁: %w", err)
	}

	current := lease.Spec.HolderIdentity
	if current != nil && *current == holder {
		// 重入：续约并返回。
		return m.Renew(ctx, key, handleFrom(lease, m.leaseDuration(), now))
	}
	if m.held(lease, now) {
		return Handle{}, fmt.Errorf("%w: %s", ErrTargetLocked, *current)
	}
	// 过期：乐观接管（更新 holder 与续约时间）。
	lease.Spec = m.leaseSpec(key, holder, now)
	if err := m.Client.Update(ctx, lease); err != nil {
		return Handle{}, fmt.Errorf("接管过期目标锁: %w", err)
	}
	return handleFrom(lease, m.leaseDuration(), now), nil
}

// Renew 续约。
func (m *KubernetesManager) Renew(ctx context.Context, key TargetKey, handle Handle) (Handle, error) {
	lease := &coordinationv1.Lease{}
	if err := m.Client.Get(ctx, leaseKey(key.Namespace, handle.LeaseName), lease); err != nil {
		return Handle{}, fmt.Errorf("读取目标锁(续约): %w", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != handle.HolderIdentity {
		return Handle{}, fmt.Errorf("%w: holder 已变化", ErrTargetLockLost)
	}
	if !m.held(lease, m.now()) {
		return Handle{}, fmt.Errorf("%w: 租约已过期", ErrTargetLockLost)
	}
	now := m.now()
	lease.Spec.RenewTime = &metav1.MicroTime{Time: now}
	if err := m.Client.Update(ctx, lease); err != nil {
		return Handle{}, fmt.Errorf("续约目标锁: %w", err)
	}
	return handleFrom(lease, m.leaseDuration(), now), nil
}

// Release 释放锁（仅 holder 本人）。
func (m *KubernetesManager) Release(ctx context.Context, key TargetKey, handle Handle) error {
	lease := &coordinationv1.Lease{}
	if err := m.Client.Get(ctx, leaseKey(key.Namespace, handle.LeaseName), lease); err != nil {
		if apierrors.IsNotFound(err) {
			return nil // 已不存在，无需释放
		}
		return fmt.Errorf("读取目标锁(释放): %w", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != handle.HolderIdentity {
		// 旧 holder 不能释放新 holder 的锁。
		return nil
	}
	if err := m.Client.Delete(ctx, lease); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("删除目标锁: %w", err)
	}
	return nil
}

// AssertHeld 写入前 fencing check。
func (m *KubernetesManager) AssertHeld(ctx context.Context, key TargetKey, holder string) error {
	lease := &coordinationv1.Lease{}
	if err := m.Client.Get(ctx, leaseKey(key.Namespace, LeaseName(key)), lease); err != nil {
		return fmt.Errorf("读取目标锁(fencing): %w", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != holder {
		return fmt.Errorf("%w: holder 已变为 %v", ErrTargetLockLost, lease.Spec.HolderIdentity)
	}
	if !m.held(lease, m.now()) {
		return fmt.Errorf("%w: 租约已过期", ErrTargetLockLost)
	}
	return nil
}

func handleFrom(lease *coordinationv1.Lease, duration time.Duration, now time.Time) Handle {
	holder := ""
	if lease.Spec.HolderIdentity != nil {
		holder = *lease.Spec.HolderIdentity
	}
	renew := now
	if lease.Spec.RenewTime != nil {
		renew = lease.Spec.RenewTime.Time
	}
	return Handle{
		LeaseName:       lease.Name,
		HolderIdentity:  holder,
		ExpiresAt:       renew.Add(duration),
		ResourceVersion: lease.ResourceVersion,
	}
}
