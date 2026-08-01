package evidence

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func rs(name, revision string, image string) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: map[string]string{rolloutRevisionAnnotation: revision},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: image}},
				},
			},
		},
	}
}

func TestLatestRevision(t *testing.T) {
	sets := []appsv1.ReplicaSet{*rs("old", "1", "v1"), *rs("new", "3", "v3"), *rs("mid", "2", "v2")}
	latest, err := LatestRevision(sets)
	if err != nil {
		t.Fatalf("LatestRevision: %v", err)
	}
	if latest.Name != "new" {
		t.Errorf("应返回 revision 3: %s", latest.Name)
	}

	if _, err := LatestRevision(nil); err == nil {
		t.Error("空列表应报错")
	}
}

func TestPreviousRevision(t *testing.T) {
	sets := []appsv1.ReplicaSet{*rs("old", "1", "v1"), *rs("new", "3", "v3"), *rs("mid", "2", "v2")}
	prev, err := PreviousRevision(sets)
	if err != nil {
		t.Fatalf("PreviousRevision: %v", err)
	}
	if prev.Name != "mid" {
		t.Errorf("应返回 revision 2: %s", prev.Name)
	}

	if _, err := PreviousRevision([]appsv1.ReplicaSet{*rs("only", "1", "v1")}); err == nil {
		t.Error("不足两个应报错")
	}
}

func TestDiffPodTemplates(t *testing.T) {
	old := rs("old", "1", "registry/checkout:v1").Spec.Template
	cur := rs("new", "2", "registry/checkout:v2").Spec.Template

	diffs, err := DiffPodTemplates(old, cur)
	if err != nil {
		t.Fatalf("DiffPodTemplates: %v", err)
	}
	foundImage := false
	for _, d := range diffs {
		if d.Field == "image" && d.Old == "registry/checkout:v1" && d.New == "registry/checkout:v2" {
			foundImage = true
		}
	}
	if !foundImage {
		t.Errorf("镜像差异缺失: %+v", diffs)
	}
}

func TestSanitizePodTemplate_NoSecretValues(t *testing.T) {
	tmpl := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      map[string]string{"app": "checkout"},
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "5"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "app",
				Image: "registry/checkout:v2",
				Env: []corev1.EnvVar{
					{Name: "DB_PASSWORD", Value: "super-secret-value"},
					{Name: "CFG", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"}, Key: "x"},
					}},
					{Name: "TOKEN", ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "app-secret"}, Key: "t"},
					}},
				},
			}},
		},
	}
	s := SanitizePodTemplate(tmpl)
	if s.Image != "registry/checkout:v2" {
		t.Errorf("image 未提取: %s", s.Image)
	}
	// 环境变量明文值绝不出现在摘要中。
	if containsAny(s.LabelsHash, "super-secret") {
		t.Error("Secret 明文泄露")
	}
	if len(s.ConfigMapRefs) != 1 || s.ConfigMapRefs[0] != "app-config" {
		t.Errorf("ConfigMap ref 错误: %+v", s.ConfigMapRefs)
	}
	if len(s.SecretRefs) != 1 || s.SecretRefs[0] != "app-secret" {
		t.Errorf("Secret ref 错误: %+v", s.SecretRefs)
	}
	if containsAny(s.AnnotationsHash, "deployment") {
		t.Error("annotation hash 不应含原文")
	}
}

func containsAny(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
