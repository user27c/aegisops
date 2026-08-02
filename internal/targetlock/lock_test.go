package targetlock

import (
	"context"
	"errors"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newTestManager(now time.Time) (*KubernetesManager, client.Client) {
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects().Build()
	m := NewKubernetesManager(c)
	m.Now = func() time.Time { return now }
	m.LeaseDuration = 60 * time.Second
	return m, c
}

func scheme() *runtimeScheme {
	return runtimeSchemeInstance
}

var keyA = TargetKey{Cluster: "c1", Namespace: "ns1", Kind: "Deployment", Name: "app-a"}
var keyB = TargetKey{Cluster: "c1", Namespace: "ns1", Kind: "Deployment", Name: "app-b"}

func TestAcquire_FirstHolder(t *testing.T) {
	m, _ := newTestManager(time.Now())
	h, err := m.Acquire(context.Background(), keyA, "uid-1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if h.HolderIdentity != "uid-1" {
		t.Errorf("holder 应为 uid-1: %s", h.HolderIdentity)
	}
	if h.LeaseName != LeaseName(keyA) {
		t.Errorf("lease 名错误: %s", h.LeaseName)
	}
}

func TestAcquire_SameTargetSecondIncidentLocked(t *testing.T) {
	m, _ := newTestManager(time.Now())
	if _, err := m.Acquire(context.Background(), keyA, "uid-1"); err != nil {
		t.Fatal(err)
	}
	_, err := m.Acquire(context.Background(), keyA, "uid-2")
	if err == nil {
		t.Fatal("第二个 Incident 应被锁定")
	}
	if !errors.Is(err, ErrTargetLocked) {
		t.Errorf("应为 ErrTargetLocked: %v", err)
	}
}

func TestAcquire_DifferentTargetConcurrent(t *testing.T) {
	m, _ := newTestManager(time.Now())
	if _, err := m.Acquire(context.Background(), keyA, "uid-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Acquire(context.Background(), keyB, "uid-2"); err != nil {
		t.Fatalf("不同目标应可并发: %v", err)
	}
}

func TestAcquire_ReentrantSameIncident(t *testing.T) {
	m, _ := newTestManager(time.Now())
	h1, err := m.Acquire(context.Background(), keyA, "uid-1")
	if err != nil {
		t.Fatal(err)
	}
	// 重入(续约)幂等。
	h2, err := m.Acquire(context.Background(), keyA, "uid-1")
	if err != nil {
		t.Fatalf("重入应成功: %v", err)
	}
	if h1.HolderIdentity != h2.HolderIdentity {
		t.Error("重入 holder 应一致")
	}
}

func TestAcquire_ExpiredLeaseTakeover(t *testing.T) {
	now := time.Now()
	m, c := newTestManager(now)
	if _, err := m.Acquire(context.Background(), keyA, "uid-1"); err != nil {
		t.Fatal(err)
	}
	// 60s 后租约过期 → uid-2 可接管。
	m.Now = func() time.Time { return now.Add(61 * time.Second) }
	h, err := m.Acquire(context.Background(), keyA, "uid-2")
	if err != nil {
		t.Fatalf("过期后应可接管: %v", err)
	}
	if h.HolderIdentity != "uid-2" {
		t.Errorf("接管后 holder 应为 uid-2: %s", h.HolderIdentity)
	}
	// 旧 holder 不能释放新 holder 的锁。
	if err := m.Release(context.Background(), keyA, Handle{LeaseName: LeaseName(keyA), HolderIdentity: "uid-1"}); err != nil {
		t.Fatal(err)
	}
	var lease coordinationv1.Lease
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: keyA.Namespace, Name: LeaseName(keyA)}, &lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != "uid-2" {
		t.Error("旧 holder 不应能删除新 holder 的锁")
	}
}

func TestRenew_AfterHolderChangeLost(t *testing.T) {
	now := time.Now()
	m, _ := newTestManager(now)
	h, err := m.Acquire(context.Background(), keyA, "uid-1")
	if err != nil {
		t.Fatal(err)
	}
	// uid-2 接管后,uid-1 续约应失败。
	m.Now = func() time.Time { return now.Add(61 * time.Second) }
	if _, err := m.Acquire(context.Background(), keyA, "uid-2"); err != nil {
		t.Fatal(err)
	}
	m.Now = func() time.Time { return now.Add(62 * time.Second) }
	_, err = m.Renew(context.Background(), keyA, h)
	if err == nil {
		t.Fatal("旧 holder 续约应失败")
	}
}

func TestAssertHeld_Fencing(t *testing.T) {
	now := time.Now()
	m, _ := newTestManager(now)
	if _, err := m.Acquire(context.Background(), keyA, "uid-1"); err != nil {
		t.Fatal(err)
	}
	if err := m.AssertHeld(context.Background(), keyA, "uid-1"); err != nil {
		t.Fatalf("持有中应通过 fencing: %v", err)
	}
	// 接管后旧 holder fencing 失败。
	m.Now = func() time.Time { return now.Add(61 * time.Second) }
	if _, err := m.Acquire(context.Background(), keyA, "uid-2"); err != nil {
		t.Fatal(err)
	}
	m.Now = func() time.Time { return now.Add(62 * time.Second) }
	if err := m.AssertHeld(context.Background(), keyA, "uid-1"); err == nil {
		t.Fatal("失锁后 fencing 应失败")
	}
}

func TestRelease_HeldByOwner(t *testing.T) {
	m, c := newTestManager(time.Now())
	h, err := m.Acquire(context.Background(), keyA, "uid-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Release(context.Background(), keyA, h); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// 释放后其他人可获取。
	if _, err := m.Acquire(context.Background(), keyA, "uid-2"); err != nil {
		t.Fatalf("释放后应可获取: %v", err)
	}
	// Release 幂等(不存在)。
	if err := m.Release(context.Background(), keyA, h); err != nil {
		t.Fatalf("重复 Release 应幂等: %v", err)
	}
	_ = c
}
