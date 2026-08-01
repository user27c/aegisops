package evidence

import (
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// rolloutRevisionAnnotation 是 ReplicaSet 的版本注解。
const rolloutRevisionAnnotation = "deployment.kubernetes.io/revision"

// FieldDiff 是 PodTemplate 的字段差异。
type FieldDiff struct {
	// Field 是字段路径。
	Field string `json:"field"`
	// Old / New 是前后值（脱敏）。
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

// SanitizedTemplate 是脱敏后的 PodTemplate 摘要。
type SanitizedTemplate struct {
	// Image 是镜像 digest/tag。
	Image string `json:"image,omitempty"`
	// Command 是命令名（不含参数细节）。
	Command []string `json:"command,omitempty"`
	// Resources 是资源值。
	Resources map[string]string `json:"resources,omitempty"`
	// ConfigMapRefs 是 ConfigMap 引用名称。
	ConfigMapRefs []string `json:"configMapRefs,omitempty"`
	// SecretRefs 只保留名称。
	SecretRefs []string `json:"secretRefs,omitempty"`
	// LabelsHash / AnnotationsHash 是哈希而非原始值。
	LabelsHash      string `json:"labelsHash,omitempty"`
	AnnotationsHash string `json:"annotationsHash,omitempty"`
}

// LatestRevision 返回版本最高的 ReplicaSet。
func LatestRevision(replicaSets []appsv1.ReplicaSet) (*appsv1.ReplicaSet, error) {
	if len(replicaSets) == 0 {
		return nil, fmt.Errorf("没有 ReplicaSet")
	}
	best := &replicaSets[0]
	for idx := range replicaSets {
		if revisionNumber(&replicaSets[idx]) > revisionNumber(best) {
			best = &replicaSets[idx]
		}
	}
	return best, nil
}

// PreviousRevision 返回版本第二高的 ReplicaSet（无则返回 nil）。
func PreviousRevision(replicaSets []appsv1.ReplicaSet) (*appsv1.ReplicaSet, error) {
	if len(replicaSets) < 2 {
		return nil, fmt.Errorf("ReplicaSet 不足两个，无法计算差异")
	}
	sorted := make([]appsv1.ReplicaSet, len(replicaSets))
	copy(sorted, replicaSets)
	sort.Slice(sorted, func(i, j int) bool {
		return revisionNumber(&sorted[i]) > revisionNumber(&sorted[j])
	})
	return &sorted[1], nil
}

// DiffPodTemplates 对比新旧模板的允许字段。
// 只保留 image/command 名/资源/probe/ConfigMap ref/label-annotation hash；
// Secret ref 只留名称。
func DiffPodTemplates(old, current corev1.PodTemplateSpec) ([]FieldDiff, error) {
	oldS := SanitizePodTemplate(old)
	newS := SanitizePodTemplate(current)

	diffs := []FieldDiff{}
	if oldS.Image != newS.Image {
		diffs = append(diffs, FieldDiff{Field: "image", Old: oldS.Image, New: newS.Image})
	}
	if len(oldS.Command) != len(newS.Command) {
		diffs = append(diffs, FieldDiff{Field: "command", Old: joinStrings(oldS.Command), New: joinStrings(newS.Command)})
	}
	for k, ov := range oldS.Resources {
		if nv, ok := newS.Resources[k]; ok && nv != ov {
			diffs = append(diffs, FieldDiff{Field: "resources." + k, Old: ov, New: nv})
		}
	}
	for k, nv := range newS.Resources {
		if _, ok := oldS.Resources[k]; !ok {
			diffs = append(diffs, FieldDiff{Field: "resources." + k, New: nv})
		}
	}
	if oldS.LabelsHash != newS.LabelsHash {
		diffs = append(diffs, FieldDiff{Field: "labelsHash", Old: oldS.LabelsHash, New: newS.LabelsHash})
	}
	if oldS.AnnotationsHash != newS.AnnotationsHash {
		diffs = append(diffs, FieldDiff{Field: "annotationsHash", Old: oldS.AnnotationsHash, New: newS.AnnotationsHash})
	}
	return diffs, nil
}

// SanitizePodTemplate 提取脱敏模板摘要（不包含 Secret 值）。
func SanitizePodTemplate(in corev1.PodTemplateSpec) SanitizedTemplate {
	out := SanitizedTemplate{Resources: map[string]string{}}
	if len(in.Spec.Containers) > 0 {
		c := in.Spec.Containers[0]
		out.Image = c.Image
		out.Command = c.Command
		if v := c.Resources.Limits.Memory(); v != nil {
			out.Resources["memoryLimit"] = v.String()
		}
		if v := c.Resources.Limits.Cpu(); v != nil {
			out.Resources["cpuLimit"] = v.String()
		}
		if v := c.Resources.Requests.Memory(); v != nil {
			out.Resources["memoryRequest"] = v.String()
		}
		for _, env := range c.Env {
			if env.ValueFrom == nil {
				continue
			}
			if ref := env.ValueFrom.ConfigMapKeyRef; ref != nil && ref.Name != "" {
				out.ConfigMapRefs = append(out.ConfigMapRefs, ref.Name)
			}
			if ref := env.ValueFrom.SecretKeyRef; ref != nil && ref.Name != "" {
				out.SecretRefs = append(out.SecretRefs, ref.Name)
			}
		}
	}
	out.LabelsHash = hashString(mapToString(in.Labels))
	out.AnnotationsHash = hashString(mapToString(in.Annotations))
	return out
}

func revisionNumber(rs *appsv1.ReplicaSet) int64 {
	if rs == nil {
		return 0
	}
	var n int64
	_, _ = fmt.Sscanf(rs.Annotations[rolloutRevisionAnnotation], "%d", &n)
	return n
}

func joinStrings(parts []string) string {
	out := ""
	for idx, p := range parts {
		if idx > 0 {
			out += ","
		}
		out += p
	}
	return out
}
