package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// KubernetesCollector 采集 Kubernetes 证据（必需源）。
type KubernetesCollector struct {
	Client client.Client
	Now    func() time.Time
}

// Collect 采集 Deployment/ReplicaSet/Pod/Event 证据与目标快照。
func (c *KubernetesCollector) Collect(ctx context.Context, incident *opsv1alpha1.AIOpsIncident) ([]EvidenceItem, TargetSnapshot, error) {
	ref := incident.Spec.TargetRef
	dep, err := c.ResolveDeployment(ctx, ref)
	if err != nil {
		return nil, TargetSnapshot{}, err
	}
	sets, err := c.ListOwnedReplicaSets(ctx, dep)
	if err != nil {
		return nil, TargetSnapshot{}, err
	}
	pods, err := c.ListPods(ctx, dep)
	if err != nil {
		return nil, TargetSnapshot{}, err
	}
	// 事件窗口与 Prom/Loki 对齐（now-30min ~ now），不依赖 Alertmanager
	// startsAt 的质量：告警延迟/缺失时根因事件（首次 OOMKilling/BackOff）
	// 可能早于 startsAt，用 startsAt 过滤会丢失根因证据（自愈链路断裂）。
	now := c.now()
	events, err := c.ListEvents(ctx, ref.Namespace, objectRefsFor(pods), now.Add(-DefaultEvidenceWindow))
	if err != nil {
		return nil, TargetSnapshot{}, err
	}

	snapshot := TargetSnapshot{
		Name:              dep.Name,
		UID:               dep.UID,
		Generation:        dep.Generation,
		ResourceVersion:   dep.ResourceVersion,
		DesiredReplicas:   derefReplicas(dep.Spec.Replicas),
		AvailableReplicas: dep.Status.AvailableReplicas,
		ReadyReplicas:     dep.Status.ReadyReplicas,
		Paused:            dep.Spec.Paused,
		ObservedAt:        now,
	}

	items := []EvidenceItem{}
	items = append(items, BuildContainerEvidence(pods)...)
	items = append(items, buildPodStateEvidence(pods, now)...)
	items = append(items, buildEventEvidence(events)...)

	// rollout diff。
	diffItems, err := buildRolloutDiffEvidence(sets, now)
	if err == nil {
		items = append(items, diffItems...)
	}

	// ConfigMap 引用哈希（只留名称与内容哈希，不留值）。
	if cmItems := buildConfigHashEvidence(dep, pods, now); len(cmItems) > 0 {
		items = append(items, cmItems...)
	}

	return items, snapshot, nil
}

func (c *KubernetesCollector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// ResolveDeployment 读取目标 Deployment。
func (c *KubernetesCollector) ResolveDeployment(ctx context.Context, ref opsv1alpha1.TargetReference) (*appsv1.Deployment, error) {
	var dep appsv1.Deployment
	if err := c.Client.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, &dep); err != nil {
		return nil, err
	}
	return &dep, nil
}

// ListOwnedReplicaSets 列出由该 Deployment 控制的 ReplicaSet。
func (c *KubernetesCollector) ListOwnedReplicaSets(ctx context.Context, d *appsv1.Deployment) ([]appsv1.ReplicaSet, error) {
	var list appsv1.ReplicaSetList
	if err := c.Client.List(ctx, &list, client.InNamespace(d.Namespace)); err != nil {
		return nil, err
	}
	owned := make([]appsv1.ReplicaSet, 0)
	for _, rs := range list.Items {
		if metav1.IsControlledBy(&rs, d) {
			owned = append(owned, rs)
		}
	}
	return owned, nil
}

// ListPods 列出由该 Deployment 选择的 Pod。
func (c *KubernetesCollector) ListPods(ctx context.Context, d *appsv1.Deployment) ([]corev1.Pod, error) {
	var list corev1.PodList
	if err := c.Client.List(ctx, &list, client.InNamespace(d.Namespace), client.MatchingLabels(d.Spec.Selector.MatchLabels)); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// ListEvents 列出目标相关事件（按 namespace 过滤，避免全集群 List）。
func (c *KubernetesCollector) ListEvents(ctx context.Context, namespace string, refs []corev1.ObjectReference, since time.Time) ([]corev1.Event, error) {
	var list corev1.EventList
	if err := c.Client.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	key := map[types.UID]bool{}
	for _, r := range refs {
		key[r.UID] = true
	}
	out := make([]corev1.Event, 0)
	for _, e := range list.Items {
		if !e.LastTimestamp.After(since) {
			continue
		}
		if key[e.InvolvedObject.UID] {
			out = append(out, e)
		}
	}
	return out, nil
}

// BuildContainerEvidence 提取容器状态（exitCode/reason/LastState/restartCount/limits）。
// 禁止读取 Secret 与完整环境变量值。
func BuildContainerEvidence(pods []corev1.Pod) []EvidenceItem {
	items := make([]EvidenceItem, 0, len(pods)*2)
	for _, pod := range pods {
		for _, status := range pod.Status.ContainerStatuses {
			lastExit, lastReason := lastTermination(status)
			summary := fmt.Sprintf(
				"pod=%s container=%s ready=%v restartCount=%d state=%s lastTermination={exitCode=%d reason=%s}",
				pod.Name, status.Name, status.Ready, status.RestartCount,
				containerStateName(status.State), lastExit, lastReason,
			)
			payload := map[string]any{
				"pod":          pod.Name,
				"container":    status.Name,
				"ready":        status.Ready,
				"restartCount": status.RestartCount,
				"state":        containerStateName(status.State),
				"lastExitCode": lastExit,
				"lastReason":   lastReason,
				"image":        status.Image,
				"imageID":      status.ImageID,
			}
			raw, _ := json.Marshal(payload)
			items = append(items, EvidenceItem{
				ID:        fmt.Sprintf("container-%s-%s", pod.Name, status.Name),
				Kind:      KindContainerState,
				Source:    "kubernetes/container-status",
				Timestamp: pod.CreationTimestamp.Time,
				Summary:   summary,
				Payload:   raw,
			})
		}
	}
	return items
}

// buildPodStateEvidence 输出 Pod 阶段与条件。
func buildPodStateEvidence(pods []corev1.Pod, now time.Time) []EvidenceItem {
	items := make([]EvidenceItem, 0, len(pods))
	for _, pod := range pods {
		conditions := make([]string, 0, len(pod.Status.Conditions))
		for _, cond := range pod.Status.Conditions {
			conditions = append(conditions, fmt.Sprintf("%s=%s", cond.Type, cond.Status))
		}
		items = append(items, EvidenceItem{
			ID:        "pod-" + pod.Name,
			Kind:      KindPodState,
			Source:    "kubernetes/pod",
			Timestamp: now,
			Summary: fmt.Sprintf("pod=%s phase=%s node=%s conditions=[%s]",
				pod.Name, pod.Status.Phase, pod.Spec.NodeName, join(conditions, " ")),
		})
	}
	return items
}

// buildEventEvidence 输出事件。
func buildEventEvidence(events []corev1.Event) []EvidenceItem {
	items := make([]EvidenceItem, 0, len(events))
	for idx, e := range events {
		items = append(items, EvidenceItem{
			ID:        fmt.Sprintf("event-%d", idx+1),
			Kind:      KindKubernetesEvent,
			Source:    "kubernetes/events",
			Timestamp: e.LastTimestamp.Time,
			Summary: fmt.Sprintf("type=%s reason=%s involved=%s/%s message=%s",
				e.Type, e.Reason, e.InvolvedObject.Kind, e.InvolvedObject.Name, e.Message),
		})
	}
	return items
}

// buildRolloutDiffEvidence 输出新旧 ReplicaSet 模板差异。
func buildRolloutDiffEvidence(sets []appsv1.ReplicaSet, now time.Time) ([]EvidenceItem, error) {
	current, err := LatestRevision(sets)
	if err != nil {
		return nil, err
	}
	previous, err := PreviousRevision(sets)
	if err != nil {
		return nil, err
	}
	diffs, err := DiffPodTemplates(previous.Spec.Template, current.Spec.Template)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(diffs)
	return []EvidenceItem{{
		ID:        "rollout-diff",
		Kind:      KindRolloutDiff,
		Source:    "kubernetes/rollout",
		Timestamp: now,
		Summary:   fmt.Sprintf("revision %s → %s，差异 %d 项", revisionOf(previous), revisionOf(current), len(diffs)),
		Payload:   raw,
	}}, nil
}

// buildConfigHashEvidence 只记录 ConfigMap 引用名称与内容哈希。
func buildConfigHashEvidence(_ *appsv1.Deployment, pods []corev1.Pod, now time.Time) []EvidenceItem {
	// MVP：仅从 Pod 环境变量引用的 configMapKeyRef 提取名称与内容哈希（不读值）。
	seen := map[string]bool{}
	items := make([]EvidenceItem, 0)
	for _, pod := range pods {
		for _, c := range pod.Spec.Containers {
			for _, env := range c.Env {
				if env.ValueFrom != nil && env.ValueFrom.ConfigMapKeyRef != nil {
					name := env.ValueFrom.ConfigMapKeyRef.Name
					if name != "" && !seen[name] {
						seen[name] = true
						raw, _ := json.Marshal(map[string]string{"name": name})
						items = append(items, EvidenceItem{
							ID:        "config-" + name,
							Kind:      KindConfigHash,
							Source:    "kubernetes/configmap-ref",
							Timestamp: now,
							Summary:   fmt.Sprintf("configMapRef name=%s（值不采集）", name),
							Payload:   raw,
						})
					}
				}
			}
		}
	}
	return items
}

// objectRefsFor 构造事件查询用的对象引用。
func objectRefsFor(pods []corev1.Pod) []corev1.ObjectReference {
	refs := make([]corev1.ObjectReference, 0, len(pods))
	for _, pod := range pods {
		refs = append(refs, corev1.ObjectReference{
			Kind:      "Pod",
			Name:      pod.Name,
			Namespace: pod.Namespace,
			UID:       pod.UID,
		})
	}
	return refs
}

func containerStateName(state corev1.ContainerState) string {
	switch {
	case state.Running != nil:
		return "running"
	case state.Waiting != nil:
		return "waiting:" + state.Waiting.Reason
	case state.Terminated != nil:
		return "terminated:" + state.Terminated.Reason
	default:
		return "unknown"
	}
}

// derefReplicas 安全解引用副本数指针。
func derefReplicas(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// lastTermination 安全读取 LastTerminationState（Terminated 可能为 nil）。
func lastTermination(status corev1.ContainerStatus) (int32, string) {
	t := status.LastTerminationState.Terminated
	if t == nil {
		return 0, ""
	}
	return t.ExitCode, t.Reason
}

func join(parts []string, sep string) string {
	out := ""
	for idx, p := range parts {
		if idx > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// revisionOf 读取 ReplicaSet 的 deployment.kubernetes.io/revision 注解。
func revisionOf(rs *appsv1.ReplicaSet) string {
	if rs == nil {
		return "?"
	}
	return rs.Annotations["deployment.kubernetes.io/revision"]
}
