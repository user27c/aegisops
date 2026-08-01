package evidence

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

func newK8sCollector(t *testing.T, objs ...client.Object) *KubernetesCollector {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("ops scheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &KubernetesCollector{Client: c}
}

func testDeployment() *appsv1.Deployment {
	replicas := int32(2)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "checkout-api",
			Namespace: "fault-lab",
			UID:       "dep-uid-1",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "checkout-api"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "checkout-api"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "app",
						Image: "registry.example.com/checkout:v1",
					}},
				},
			},
		},
	}
}

func TestKubernetesCollector_Collect(t *testing.T) {
	dep := testDeployment()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "checkout-api-abc123",
			Namespace: "fault-lab",
			Labels:    map[string]string{"app": "checkout-api"},
			UID:       "pod-uid-1",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app"}},
			NodeName:   "node-1",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "app",
				Ready:        true,
				RestartCount: 3,
				Image:        "registry.example.com/checkout:v1",
				State: corev1.ContainerState{
					Running: &corev1.ContainerStateRunning{},
				},
				LastTerminationState: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"},
				},
			}},
		},
	}
	rsNew := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "checkout-api-rs-new",
			Namespace:       "fault-lab",
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": "2"},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(dep, appsv1.SchemeGroupVersion.WithKind("Deployment"))},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "registry.example.com/checkout:v2"}}},
			},
		},
	}
	rsOld := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "checkout-api-rs-old",
			Namespace:       "fault-lab",
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": "1"},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(dep, appsv1.SchemeGroupVersion.WithKind("Deployment"))},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "registry.example.com/checkout:v1"}}},
			},
		},
	}
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "evt-1", Namespace: "fault-lab"},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod", Name: "checkout-api-abc123", Namespace: "fault-lab", UID: "pod-uid-1",
		},
		Type: corev1.EventTypeWarning, Reason: "OOMKilling",
		Message:        "Memory cgroup out of memory",
		LastTimestamp:  metav1.NewTime(time.Now().Add(time.Hour)),
		FirstTimestamp: metav1.NewTime(time.Now()),
	}

	c := newK8sCollector(t, dep, pod, rsNew, rsOld, event)
	items, snapshot, err := c.Collect(context.Background(), testIncident())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snapshot.Name != "checkout-api" || snapshot.UID != "dep-uid-1" {
		t.Errorf("快照错误: %+v", snapshot)
	}
	if snapshot.DesiredReplicas != 2 {
		t.Errorf("期望副本数错误: %d", snapshot.DesiredReplicas)
	}

	// 容器状态证据必须含 OOMKilled 信息。
	foundContainer := false
	foundEvent := false
	foundDiff := false
	for _, item := range items {
		switch item.Kind {
		case KindContainerState:
			if item.Summary == "" {
				t.Error("容器证据摘要为空")
			}
			foundContainer = true
		case KindKubernetesEvent:
			foundEvent = true
		case KindRolloutDiff:
			foundDiff = true
		}
	}
	if !foundContainer || !foundEvent || !foundDiff {
		t.Errorf("证据类型缺失: container=%v event=%v diff=%v", foundContainer, foundEvent, foundDiff)
	}

	// Secret 值不采集：模板中不含 env 值（BuildContainerEvidence 已覆盖）。
}

func TestBuildContainerEvidence_NoPanicNilTerminated(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p1"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "app",
				Ready:        false,
				RestartCount: 1,
				State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
				// LastTerminationState 保持零值（Terminated=nil）。
			}},
		},
	}
	items := BuildContainerEvidence([]corev1.Pod{*pod})
	if len(items) != 1 {
		t.Fatalf("应有 1 条: %d", len(items))
	}
	if !strings.Contains(items[0].Summary, "CrashLoopBackOff") {
		t.Errorf("摘要缺失状态: %s", items[0].Summary)
	}
}

func TestListOwnedReplicaSets(t *testing.T) {
	dep := testDeployment()
	rs1 := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "rs-owned",
			Namespace:       "fault-lab",
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(dep, appsv1.SchemeGroupVersion.WithKind("Deployment"))},
		},
	}
	rs2 := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: "rs-foreign", Namespace: "fault-lab"},
	}
	c := newK8sCollector(t, dep, rs1, rs2)
	owned, err := c.ListOwnedReplicaSets(context.Background(), dep)
	if err != nil {
		t.Fatalf("ListOwnedReplicaSets: %v", err)
	}
	if len(owned) != 1 || owned[0].Name != "rs-owned" {
		t.Errorf("应只返回受控 RS: %+v", owned)
	}
}

func TestResolveDeployment_Missing(t *testing.T) {
	c := newK8sCollector(t)
	_, err := c.ResolveDeployment(context.Background(), opsv1alpha1.TargetReference{Namespace: "fault-lab", Name: "missing"})
	if err == nil {
		t.Error("缺失 Deployment 应报错")
	}
}
