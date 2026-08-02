package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/evidence"
)

// testClock 是固定时钟。
type testClock struct{ now time.Time }

func (t *testClock) Now() time.Time { return t.now }
func (t *testClock) Sleep(d time.Duration) { //nolint:revive -- 测试时钟实现
	t.now = t.now.Add(d)
}
func (t *testClock) After(d time.Duration) <-chan time.Time { //nolint:revive -- 测试时钟实现
	ch := make(chan time.Time, 1)
	ch <- t.now.Add(d)
	return ch
}
func (t *testClock) AfterFunc(_ time.Duration, _ func()) clock.Timer {
	return nil
}
func (t *testClock) NewTicker(_ time.Duration) clock.Ticker { return nil }
func (t *testClock) NewTimer(_ time.Duration) clock.Timer   { return nil }
func (t *testClock) Tick(d time.Duration) <-chan time.Time  { return t.After(d) }
func (t *testClock) Since(x time.Time) time.Duration        { return t.now.Sub(x) }
func (t *testClock) Until(x time.Time) time.Duration        { return x.Sub(t.now) }

// fakeCollector 返回固定证据包。
type fakeCollector struct {
	hash  string
	fail  bool
	calls int
}

var errBoom = errors.New("boom")

func (f *fakeCollector) Collect(_ context.Context, _ *opsv1alpha1.AIOpsIncident) (evidence.EvidencePack, error) {
	f.calls++
	if f.fail {
		return evidence.EvidencePack{}, errBoom
	}
	return evidence.EvidencePack{
		SchemaVersion: "v1",
		Hash:          f.hash,
		Window: evidence.TimeWindow{
			Start: time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC),
			End:   time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		},
		Items: []evidence.EvidenceItem{{ID: "k8s-1", Kind: evidence.KindKubernetesEvent, Summary: "event"}},
	}, nil
}

func newReconciler(t *testing.T, collector evidence.Collector, objs ...client.Object) (*IncidentReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("ops scheme: %v", err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.AIOpsIncident{}).
		WithObjects(objs...).
		Build()
	r := &IncidentReconciler{
		Client:                  c,
		Collector:               collector,
		Clock:                   &testClock{now: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)},
		RequeueEvidenceInterval: 30 * time.Second,
		RequeueStuckInterval:    30 * time.Second,
	}
	return r, c
}

func targetDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-api", Namespace: "fault-lab", UID: "dep-1"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "checkout"}},
		},
	}
}

func firingIncident() *opsv1alpha1.AIOpsIncident {
	return &opsv1alpha1.AIOpsIncident{
		ObjectMeta: metav1.ObjectMeta{Name: "incident-1", Namespace: "fault-lab"},
		Spec: opsv1alpha1.AIOpsIncidentSpec{
			Fingerprint:  "sha256:" + repeatChar('a', 64),
			Cluster:      "local-k3s",
			AlertName:    "ContainerOOMKilled",
			Severity:     "critical",
			SourceStatus: "firing",
			TargetRef: opsv1alpha1.TargetReference{
				APIVersion: "apps/v1", Kind: "Deployment",
				Namespace: "fault-lab", Name: "checkout-api",
			},
			StartedAt:      metav1.NewTime(time.Now()),
			LastReceivedAt: metav1.NewTime(time.Now()),
		},
	}
}

func repeatChar(c rune, n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = c
	}
	return string(out)
}

func reconcileOnce(t *testing.T, r *IncidentReconciler, name string) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Namespace: "fault-lab", Name: name},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return res
}

func TestReconcile_NotFound(t *testing.T) {
	r, _ := newReconciler(t, nil)
	res, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Namespace: "fault-lab", Name: "missing"},
	})
	if err != nil {
		t.Fatalf("NotFound 不应报错: %v", err)
	}
	if res.RequeueAfter > 0 {
		t.Error("NotFound 不应 requeue")
	}
}

func TestReconcile_TerminalNoop(t *testing.T) {
	incident := firingIncident()
	incident.Status.Phase = opsv1alpha1.PhaseResolved
	r, c := newReconciler(t, nil, incident)

	res := reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter > 0 {
		t.Error("终态不应 requeue")
	}
	// 状态未被改动。
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "incident-1"}, &got)
	if got.Status.Phase != opsv1alpha1.PhaseResolved {
		t.Error("终态不应被修改")
	}
}

func TestReconcile_DetectedToCollecting(t *testing.T) {
	incident := firingIncident()
	collector := &fakeCollector{hash: "hash-1"}
	r, c := newReconciler(t, collector, incident, targetDeployment())

	res := reconcileOnce(t, r, "incident-1")

	// 第一次 reconcile：建 finalizer。
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "incident-1"}, &got)
	if !containsString(got.Finalizers, FinalizerName) {
		t.Error("finalizer 未建立")
	}
	_ = res

	// 第二次 reconcile：Detected 检查（目标存在、resolved 判断）。
	reconcileOnce(t, r, "incident-1")

	// 第三次 reconcile：进入 CollectingEvidence 并采集。
	res = reconcileOnce(t, r, "incident-1")
	if res.RequeueAfter != 30*time.Second {
		t.Errorf("应 requeue 30s: %v", res.RequeueAfter)
	}
	got = opsv1alpha1.AIOpsIncident{}
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "incident-1"}, &got)
	if got.Status.Phase != opsv1alpha1.PhaseCollectingEvidence {
		t.Errorf("Phase 应为 CollectingEvidence: %s", got.Status.Phase)
	}
	if collector.calls != 1 {
		t.Errorf("Collector 应调用 1 次: %d", collector.calls)
	}
	if got.Status.Evidence == nil || got.Status.Evidence.Hash != "hash-1" {
		t.Errorf("证据摘要未写入: %+v", got.Status.Evidence)
	}
}

func TestReconcile_EvidenceNotDuplicated(t *testing.T) {
	incident := firingIncident()
	collector := &fakeCollector{hash: "hash-1"}
	r, c := newReconciler(t, collector, incident, targetDeployment())

	reconcileOnce(t, r, "incident-1") // finalizer
	reconcileOnce(t, r, "incident-1") // Detected
	reconcileOnce(t, r, "incident-1") // 采集
	reconcileOnce(t, r, "incident-1") // 再次采集

	// hash 相同：Status.Evidence 不重复写入、Timeline 不重复追加。
	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "incident-1"}, &got)
	if got.Status.Evidence == nil || got.Status.Evidence.Hash != "hash-1" {
		t.Errorf("证据摘要异常: %+v", got.Status.Evidence)
	}
	transitionCount := 0
	for _, e := range got.Status.Timeline {
		if e.Type == "PhaseTransition" {
			transitionCount++
		}
	}
	if transitionCount != 1 {
		t.Errorf("PhaseTransition 应只有 1 次: %d", transitionCount)
	}
}

func TestReconcile_ResolvedWithoutAction(t *testing.T) {
	incident := firingIncident()
	incident.Spec.SourceStatus = "resolved"
	r, c := newReconciler(t, &fakeCollector{hash: "h"}, incident, targetDeployment())

	reconcileOnce(t, r, "incident-1") // finalizer
	reconcileOnce(t, r, "incident-1") // Detected(resolved 判断)

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "incident-1"}, &got)
	if got.Status.Phase != opsv1alpha1.PhaseRecoveredNoAction {
		t.Errorf("resolved 应转 RecoveredWithoutAction: %s", got.Status.Phase)
	}
}

func TestReconcile_TargetMissing(t *testing.T) {
	incident := firingIncident()
	r, c := newReconciler(t, nil, incident)

	reconcileOnce(t, r, "incident-1") // finalizer
	reconcileOnce(t, r, "incident-1") // Detected(目标检查失败)

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "incident-1"}, &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("目标缺失应 Escalated: %s", got.Status.Phase)
	}
}

func TestReconcile_CollectFailed(t *testing.T) {
	incident := firingIncident()
	collector := &fakeCollector{hash: "h", fail: true}
	r, c := newReconciler(t, collector, incident, targetDeployment())

	reconcileOnce(t, r, "incident-1") // finalizer
	reconcileOnce(t, r, "incident-1") // Detected
	reconcileOnce(t, r, "incident-1") // 采集失败

	var got opsv1alpha1.AIOpsIncident
	_ = c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "incident-1"}, &got)
	if got.Status.Phase != opsv1alpha1.PhaseEscalated {
		t.Errorf("采集失败应 Escalated: %s", got.Status.Phase)
	}
}

func TestReconcile_DeletionRemovesFinalizer(t *testing.T) {
	incident := firingIncident()
	incident.Finalizers = []string{FinalizerName}
	now := metav1.Now()
	incident.DeletionTimestamp = &now
	r, c := newReconciler(t, nil, incident)

	reconcileOnce(t, r, "incident-1")

	var got opsv1alpha1.AIOpsIncident
	err := c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: "incident-1"}, &got)
	if err == nil {
		if containsString(got.Finalizers, FinalizerName) {
			t.Error("finalizer 应被移除")
		}
	}
}

func TestReconciler_SetupWithManagerNil(t *testing.T) {
	r := &IncidentReconciler{}
	if err := r.SetupWithManager(nil); err == nil {
		t.Error("nil manager 应报错")
	}
}
