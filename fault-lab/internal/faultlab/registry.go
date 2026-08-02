// Package faultlab 实现受控故障注入。
//
// 安全边界：注入只影响本进程；/inject 需要 CHAOS_ENABLED=true（默认关闭）；
// 注入时长有上限；/recover 与 /cleanup 始终可用。
package faultlab

import (
	"fmt"
	"sync"
	"time"
)

// Injector 是故障注入器接口。
type Injector interface {
	// Type 返回故障类型。
	Type() string
	// Inject 开始故障（幂等：已注入时返回 nil）。
	Inject(duration time.Duration) error
	// Recover 停止故障（幂等）。
	Recover() error
	// Status 返回当前状态描述。
	Status() string
}

// Registry 是注入器注册表。
type Registry struct {
	mu         sync.Mutex
	injectors  map[string]Injector
	chaos      bool
	maxDuration time.Duration
}

// NewRegistry 创建注入器注册表。
func NewRegistry(chaosEnabled bool, maxDuration time.Duration) *Registry {
	if maxDuration <= 0 {
		maxDuration = 10 * time.Minute
	}
	return &Registry{
		injectors:   map[string]Injector{},
		chaos:       chaosEnabled,
		maxDuration: maxDuration,
	}
}

// Register 注册注入器。
func (r *Registry) Register(i Injector) error {
	if i == nil || i.Type() == "" {
		return fmt.Errorf("注入器不能为空")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.injectors[i.Type()]; ok {
		return fmt.Errorf("注入器 %s 已注册", i.Type())
	}
	r.injectors[i.Type()] = i
	return nil
}

// Inject 注入故障。chaos 未启用或类型未知时拒绝。
func (r *Registry) Inject(typ string, duration time.Duration) error {
	if !r.chaos {
		return fmt.Errorf("chaos 未启用（CHAOS_ENABLED=false）")
	}
	if duration <= 0 || duration > r.maxDuration {
		return fmt.Errorf("注入时长必须在 1s~%s 之间", r.maxDuration)
	}
	r.mu.Lock()
	i, ok := r.injectors[typ]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("未知注入类型 %q（可用: %v）", typ, r.Types())
	}
	return i.Inject(duration)
}

// Recover 恢复故障。
func (r *Registry) Recover(typ string) error {
	r.mu.Lock()
	i, ok := r.injectors[typ]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("未知注入类型 %q", typ)
	}
	return i.Recover()
}

// Cleanup 恢复全部故障。
func (r *Registry) Cleanup() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for _, i := range r.injectors {
		if err := i.Recover(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Types 返回已注册注入类型。
func (r *Registry) Types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.injectors))
	for t := range r.injectors {
		out = append(out, t)
	}
	return out
}

// Status 返回全部注入器状态。
func (r *Registry) Status() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for t, i := range r.injectors {
		out[t] = i.Status()
	}
	return out
}
