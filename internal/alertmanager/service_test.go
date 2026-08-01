package alertmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// fakeClock 提供可控时间。
type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time { return f.now }
func (f *fakeClock) Sleep(d time.Duration) {
	f.now = f.now.Add(d)
}
func (f *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- f.now.Add(d)
	return ch
}
func (f *fakeClock) AfterFunc(_ time.Duration, _ func()) clock.Timer {
	return nil
}
func (f *fakeClock) Tick(d time.Duration) <-chan time.Time {
	return f.After(d)
}
func (f *fakeClock) NewTicker(_ time.Duration) clock.Ticker { return nil }
func (f *fakeClock) NewTimer(_ time.Duration) clock.Timer   { return nil }
func (f *fakeClock) Since(t time.Time) time.Duration        { return f.now.Sub(t) }
func (f *fakeClock) Until(t time.Time) time.Duration        { return t.Sub(f.now) }

// countingMetrics 记录指标调用。
type countingMetrics struct {
	created, deduped, rejected int
}

func (m *countingMetrics) IncidentsCreated()        { m.created++ }
func (m *countingMetrics) IncidentsDeduplicated()   { m.deduped++ }
func (m *countingMetrics) IncidentsRejected(string) { m.rejected++ }

// fakeResolver 直接返回固定 UID。
type fakeResolver struct{ uid types.UID }

func (r *fakeResolver) ResolveTargetUID(_ context.Context, _ opsv1alpha1.TargetReference) (types.UID, error) {
	return r.uid, nil
}

// missingResolver 模拟目标不存在。
type missingResolver struct{}

func (r *missingResolver) ResolveTargetUID(context.Context, opsv1alpha1.TargetReference) (types.UID, error) {
	return "", errors.New("deployments.apps \"w\" not found")
}

func newTestEnv(t *testing.T) (client.Client, *fakeClock, *countingMetrics) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("添加 clientgo scheme: %v", err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("添加 ops scheme: %v", err)
	}
	clk := &fakeClock{now: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)}
	metrics := &countingMetrics{}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.AIOpsIncident{}).
		Build()
	return c, clk, metrics
}

func TestUpsert_NewFiring(t *testing.T) {
	c, clk, _ := newTestEnv(t)
	writer := NewKubernetesWriter(c, clk)
	a := sampleAlert("ContainerOOMKilled")

	res, err := writer.UpsertFromAlert(context.Background(), a)
	if err != nil {
		t.Fatalf("UpsertFromAlert 失败: %v", err)
	}
	if !res.Created {
		t.Error("首次应创建")
	}

	var incident opsv1alpha1.AIOpsIncident
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: res.IncidentName}, &incident); err != nil {
		t.Fatalf("读取 Incident 失败: %v", err)
	}
	if incident.Spec.Fingerprint == "" || incident.Spec.TargetRef.UID != "u-1" {
		t.Errorf("Incident spec 错误: %+v", incident.Spec)
	}
	if incident.Spec.SourceStatus != StatusFiring {
		t.Errorf("SourceStatus 应为 firing: %s", incident.Spec.SourceStatus)
	}
	if incident.Status.Phase != opsv1alpha1.PhaseDetected {
		t.Errorf("Phase 应为 Detected: %s", incident.Status.Phase)
	}
}

func TestUpsert_DuplicateFiring(t *testing.T) {
	c, clk, _ := newTestEnv(t)
	writer := NewKubernetesWriter(c, clk)
	a := sampleAlert("ContainerOOMKilled")

	first, err := writer.UpsertFromAlert(context.Background(), a)
	if err != nil {
		t.Fatalf("首次失败: %v", err)
	}

	// 同一指纹再次 firing → 去重更新，不新建。
	res, err := writer.UpsertFromAlert(context.Background(), a)
	if err != nil {
		t.Fatalf("重复失败: %v", err)
	}
	if res.Created || !res.Updated {
		t.Error("重复 firing 应更新而非创建")
	}
	if res.IncidentName != first.IncidentName {
		t.Errorf("重复应命中同一 Incident: %s vs %s", res.IncidentName, first.IncidentName)
	}

	var list opsv1alpha1.AIOpsIncidentList
	if err := c.List(context.Background(), &list, client.InNamespace("fault-lab")); err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("应只有 1 个 Incident，得到 %d", len(list.Items))
	}
}

func TestUpsert_ResolvedUpdatesSpec(t *testing.T) {
	c, clk, _ := newTestEnv(t)
	writer := NewKubernetesWriter(c, clk)
	a := sampleAlert("ContainerOOMKilled")

	if _, err := writer.UpsertFromAlert(context.Background(), a); err != nil {
		t.Fatalf("首次失败: %v", err)
	}

	// resolved 到达 → 更新 SourceStatus 与 ResolvedAt，不创建新对象。
	resolved := sampleAlert("ContainerOOMKilled")
	resolved.Status = StatusResolved
	res, err := writer.UpsertFromAlert(context.Background(), resolved)
	if err != nil {
		t.Fatalf("resolved 失败: %v", err)
	}
	if res.Created {
		t.Error("resolved 不应创建新对象")
	}

	var incident opsv1alpha1.AIOpsIncident
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: res.IncidentName}, &incident); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if incident.Spec.SourceStatus != StatusResolved {
		t.Errorf("SourceStatus 应为 resolved: %s", incident.Spec.SourceStatus)
	}
	if incident.Spec.ResolvedAt == nil {
		t.Error("ResolvedAt 应被设置")
	}
	// 不直接修改 Phase（终态由 Controller 决定）。
	if incident.Status.Phase == opsv1alpha1.PhaseResolved {
		t.Error("gateway 不得直接写终态 Phase")
	}
}

func TestUpsert_NewEpisodeAfterTerminal(t *testing.T) {
	c, clk, _ := newTestEnv(t)
	writer := NewKubernetesWriter(c, clk)
	a := sampleAlert("ContainerOOMKilled")

	first, err := writer.UpsertFromAlert(context.Background(), a)
	if err != nil {
		t.Fatalf("首次失败: %v", err)
	}

	// 模拟 Controller 已把 Incident 置为终态。
	var incident opsv1alpha1.AIOpsIncident
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "fault-lab", Name: first.IncidentName}, &incident); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	patch := client.MergeFrom(incident.DeepCopy())
	incident.Status.Phase = opsv1alpha1.PhaseResolved
	if err := c.Status().Patch(context.Background(), &incident, patch); err != nil {
		t.Fatalf("置终态失败: %v", err)
	}

	// 终态后再 firing → 新 episode。
	res, err := writer.UpsertFromAlert(context.Background(), a)
	if err != nil {
		t.Fatalf("新 episode 失败: %v", err)
	}
	if !res.Created {
		t.Error("终态后新 firing 应创建新 episode")
	}
	if res.IncidentName == first.IncidentName {
		t.Errorf("episode 名称应不同: %s", res.IncidentName)
	}
}

func TestService_ProcessSummary(t *testing.T) {
	c, clk, metrics := newTestEnv(t)
	writer := NewKubernetesWriter(c, clk)
	svc := NewService("cluster-a", writer, &fakeResolver{uid: "u-1"}, clk, metrics)

	labels := map[string]string{"alertname": "A", "namespace": "fault-lab", "workload": "checkout"}
	hook := Webhook{
		GroupKey: "{}",
		Status:   "firing",
		Alerts: []Alert{
			{Status: "firing", Labels: labels, StartsAt: time.Now(), Fingerprint: "fp-a"},
			{Status: "firing", Labels: labels, StartsAt: time.Now(), Fingerprint: "fp-a"},
		},
	}
	result, err := svc.Process(context.Background(), hook)
	if err != nil {
		t.Fatalf("Process 失败: %v", err)
	}
	if result.Accepted != 2 || result.Deduplicated != 1 || result.Rejected != 0 {
		t.Errorf("汇总错误: %+v", result)
	}
	if metrics.created != 1 || metrics.deduped != 1 {
		t.Errorf("指标错误: created=%d deduped=%d", metrics.created, metrics.deduped)
	}
}

func TestService_RejectsUnknownTarget(t *testing.T) {
	c, clk, metrics := newTestEnv(t)
	writer := NewKubernetesWriter(c, clk)
	svc := NewService("cluster-a", writer, &missingResolver{}, clk, metrics)

	hook := Webhook{
		Status: "firing",
		Alerts: []Alert{{Status: "firing", Labels: map[string]string{"alertname": "A", "namespace": "n", "workload": "w"}, Fingerprint: "fp"}},
	}
	result, err := svc.Process(context.Background(), hook)
	if err != nil {
		t.Fatalf("Process 失败: %v", err)
	}
	if result.Rejected != 1 || result.Accepted != 0 {
		t.Errorf("目标不存在应拒绝: %+v", result)
	}
	if metrics.rejected != 1 {
		t.Errorf("拒绝指标应为 1: %d", metrics.rejected)
	}
}
