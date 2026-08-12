package faultlab

import (
	"errors"
	"testing"
	"time"
)

func newTestRegistry() *Registry {
	r := NewRegistry(true, time.Minute)
	for _, i := range []Injector{
		&OOMInjector{}, &CrashLoopInjector{}, &ConfigInjector{},
		&CPUInjector{}, &DependencyInjector{},
	} {
		if err := r.Register(i); err != nil {
			panic(err)
		}
	}
	return r
}

func TestCrashLoopInjectorRequestsProcessTermination(t *testing.T) {
	r := newTestRegistry()
	err := r.Inject("crashloop", time.Minute)
	if !errors.Is(err, ErrProcessTermination) {
		t.Fatalf("crashloop 应请求 host 进程退出，实际: %v", err)
	}
}

func TestRegistry_ChaosGate(t *testing.T) {
	// chaos 关闭时注入必须拒绝。
	r := NewRegistry(false, time.Minute)
	_ = r.Register(&ConfigInjector{})
	if err := r.Inject("config", time.Minute); err == nil {
		t.Fatal("chaos 未启用时必须拒绝注入")
	}
	// 恢复不受 gate 限制。
	if err := r.Recover("config"); err != nil {
		t.Fatalf("恢复不应被 gate 限制: %v", err)
	}
}

func TestRegistry_DurationBound(t *testing.T) {
	r := newTestRegistry()
	if err := r.Inject("config", 0); err == nil {
		t.Error("0 时长应拒绝")
	}
	if err := r.Inject("config", 2*time.Minute); err == nil {
		t.Error("超过最大时长应拒绝")
	}
}

func TestRegistry_UnknownType(t *testing.T) {
	r := newTestRegistry()
	if err := r.Inject("unknown", time.Minute); err == nil {
		t.Error("未知类型应拒绝")
	}
	if err := r.Recover("unknown"); err == nil {
		t.Error("未知类型恢复应拒绝")
	}
}

func TestConfigInjector_Cycle(t *testing.T) {
	r := newTestRegistry()
	if err := r.Inject("config", time.Minute); err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	cfg := r.injectors["config"].(*ConfigInjector)
	if !cfg.Active() {
		t.Error("注入后应 active")
	}
	if err := r.Recover("config"); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if cfg.Active() {
		t.Error("恢复后不应 active")
	}
	// 重复注入幂等。
	if err := r.Inject("config", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := r.Inject("config", time.Minute); err != nil {
		t.Fatalf("重复注入应幂等: %v", err)
	}
}

func TestDependencyInjector_Cycle(t *testing.T) {
	r := newTestRegistry()
	if err := r.Inject("dependency", 5*time.Second); err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	dep := r.injectors["dependency"].(*DependencyInjector)
	if dep.Latency() != 5*time.Second {
		t.Errorf("延迟错误: %v", dep.Latency())
	}
	if err := r.Recover("dependency"); err != nil {
		t.Fatal(err)
	}
	if dep.Latency() != 0 {
		t.Error("恢复后延迟应为 0")
	}
}

func TestCPUInjector_Cycle(t *testing.T) {
	r := newTestRegistry()
	if err := r.Inject("cpu", time.Minute); err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if err := r.Recover("cpu"); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	// 重复恢复幂等。
	if err := r.Recover("cpu"); err != nil {
		t.Fatalf("重复恢复应幂等: %v", err)
	}
}

func TestOOMInjector_Cycle(t *testing.T) {
	r := newTestRegistry()
	if err := r.Inject("oom", time.Minute); err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if err := r.Recover("oom"); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if err := r.Recover("oom"); err != nil {
		t.Fatalf("重复恢复应幂等: %v", err)
	}
}

func TestCleanup(t *testing.T) {
	r := newTestRegistry()
	_ = r.Inject("config", time.Minute)
	_ = r.Inject("cpu", time.Minute)
	if err := r.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	for typ, status := range r.Status() {
		if status != "ok" {
			t.Errorf("%s 清理后应为 ok: %s", typ, status)
		}
	}
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	r := NewRegistry(true, time.Minute)
	_ = r.Register(&ConfigInjector{})
	if err := r.Register(&ConfigInjector{}); err == nil {
		t.Error("重复注册应报错")
	}
	if err := r.Register(nil); err == nil {
		t.Error("nil 注入器应报错")
	}
}
