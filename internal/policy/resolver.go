package policy

import (
	"context"
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// Resolver 解析 Incident 适用的 RemediationPolicy。
type Resolver interface {
	// Resolve 返回命中的策略；无匹配返回 nil（调用方 → Deny）。
	Resolve(ctx context.Context, i *opsv1alpha1.AIOpsIncident, target client.Object) (*opsv1alpha1.RemediationPolicy, error)
}

// Matches 判断策略是否匹配目标。
// namespaceLabels/workloadLabels 是目标的标签；kind 是目标类型（Deployment）。
func Matches(policy *opsv1alpha1.RemediationPolicy, namespaceLabels, workloadLabels map[string]string, kind string) (bool, error) {
	sel := policy.Spec.TargetSelector
	if len(sel.Kinds) > 0 {
		found := false
		for _, k := range sel.Kinds {
			if k == kind {
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
	}
	if sel.NamespaceSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(sel.NamespaceSelector)
		if err != nil {
			return false, fmt.Errorf("namespaceSelector 非法: %w", err)
		}
		if !selector.Matches(labels.Set(namespaceLabels)) {
			return false, nil
		}
	}
	if sel.WorkloadSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(sel.WorkloadSelector)
		if err != nil {
			return false, fmt.Errorf("workloadSelector 非法: %w", err)
		}
		if !selector.Matches(labels.Set(workloadLabels)) {
			return false, nil
		}
	}
	return true, nil
}

// SelectHighestPriority 选择优先级最高的策略；并列返回错误（fail closed）。
func SelectHighestPriority(candidates []opsv1alpha1.RemediationPolicy) (*opsv1alpha1.RemediationPolicy, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Spec.Priority > candidates[j].Spec.Priority
	})
	best := &candidates[0]
	for idx := 1; idx < len(candidates); idx++ {
		if candidates[idx].Spec.Priority == best.Spec.Priority {
			return nil, fmt.Errorf("策略优先级并列 %d：%s 与 %s", best.Spec.Priority, best.Name, candidates[idx].Name)
		}
	}
	return best, nil
}
