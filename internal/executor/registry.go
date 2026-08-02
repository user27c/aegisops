package executor

import (
	"fmt"
	"sync"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// Registry 是动作注册表。
type Registry struct {
	mu      sync.RWMutex
	actions map[opsv1alpha1.ActionType]Action
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{actions: map[opsv1alpha1.ActionType]Action{}}
}

// Register 注册动作；重复注册返回错误（防覆盖）。
func (r *Registry) Register(a Action) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a == nil || a.Type() == "" {
		return fmt.Errorf("动作不能为空")
	}
	if _, ok := r.actions[a.Type()]; ok {
		return fmt.Errorf("动作 %s 已注册", a.Type())
	}
	r.actions[a.Type()] = a
	return nil
}

// Get 返回动作；未注册返回错误（fail closed）。
func (r *Registry) Get(t opsv1alpha1.ActionType) (Action, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.actions[t]
	if !ok {
		return nil, fmt.Errorf("动作 %s 未注册", t)
	}
	return a, nil
}

// Names 返回全部已注册动作名。
func (r *Registry) Names() []opsv1alpha1.ActionType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]opsv1alpha1.ActionType, 0, len(r.actions))
	for t := range r.actions {
		out = append(out, t)
	}
	return out
}

// DefaultRegistry 返回注册了全部 5 个动作的注册表。
func DefaultRegistry() (*Registry, error) {
	r := NewRegistry()
	for _, a := range []Action{
		&RestartWorkloadAction{},
		&ScaleDeploymentAction{},
		&PatchResourceLimitAction{},
		&RollbackDeploymentAction{},
		&RestoreConfigMapAction{},
	} {
		if err := r.Register(a); err != nil {
			return nil, err
		}
	}
	return r, nil
}
