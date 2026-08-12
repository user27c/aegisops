package controller

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/observability"
)

func metricsScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("ops scheme: %v", err)
	}
	return scheme
}

func metricsClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(metricsScheme(t)).
		WithObjects(objs...).
		Build()
}

func activeIncident(name string, phase opsv1alpha1.IncidentPhase, severity string, created time.Time, timeline []opsv1alpha1.TimelineEntry) *opsv1alpha1.AIOpsIncident {
	return &opsv1alpha1.AIOpsIncident{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "aegisops-system",
			CreationTimestamp: metav1.NewTime(created),
		},
		Spec: opsv1alpha1.AIOpsIncidentSpec{
			Severity: severity,
		},
		Status: opsv1alpha1.AIOpsIncidentStatus{
			Phase:    phase,
			Timeline: timeline,
		},
	}
}

func TestIncidentMetricsReconciler_Recompute_NilMetrics(t *testing.T) {
	r := &IncidentMetricsReconciler{
		Client:  metricsClient(t),
		Metrics: nil,
		Logger:  logr.Discard(),
	}
	// 不 panic，直接返回。
	r.recompute(context.Background())
}

func TestIncidentMetricsReconciler_Recompute(t *testing.T) {
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	objs := []client.Object{
		// 同 phase 两个活跃事故：oldest 应选时间更早(有 timeline 的第二个)的那个。
		activeIncident("detected-a", opsv1alpha1.PhaseDetected, "warning", now.Add(-time.Minute), nil),
		activeIncident("detected-b", opsv1alpha1.PhaseDetected, "critical", now.Add(-2*time.Minute), []opsv1alpha1.TimelineEntry{
			{Time: metav1.NewTime(now.Add(-90 * time.Second))},
		}),
		// 空 phase 默认归入 Detected。
		activeIncident("empty-phase", "", "info", now, nil),
		// 终端事故被跳过。
		activeIncident("resolved", opsv1alpha1.PhaseResolved, "critical", now.Add(-time.Hour), nil),
	}

	m, err := observability.NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	r := &IncidentMetricsReconciler{
		Client:  metricsClient(t, objs...),
		Metrics: m,
		Logger:  logr.Discard(),
	}
	r.recompute(context.Background())

	// 两个 Detected + 一个空 phase(归 Detected) = 3 个活跃；Resolved 被跳过。
	if got := testutil.ToFloat64(m.ActiveIncidents.WithLabelValues(string(opsv1alpha1.PhaseDetected), "warning")); got != 1 {
		t.Errorf("Detected/warning 活跃数 = %v, 期望 1", got)
	}
	if got := testutil.ToFloat64(m.ActiveIncidents.WithLabelValues(string(opsv1alpha1.PhaseDetected), "critical")); got != 1 {
		t.Errorf("Detected/critical 活跃数 = %v, 期望 1", got)
	}
	if got := testutil.ToFloat64(m.ActiveIncidents.WithLabelValues(string(opsv1alpha1.PhaseDetected), "info")); got != 1 {
		t.Errorf("Detected/info(空 phase 默认) 活跃数 = %v, 期望 1", got)
	}
	// 终端事故不得计入任何 phase。
	if got := testutil.ToFloat64(m.ActiveIncidents.WithLabelValues(string(opsv1alpha1.PhaseResolved), "critical")); got != 0 {
		t.Errorf("Resolved 不应计入活跃, 得到 %v", got)
	}

	// oldest 聚合:detected-b 的 timeline 最后一条是 now-90s,早于 detected-a 的 now-60s,
	// 故 Detected 的 oldest 应绑定 severity=critical 且年龄为正。
	age := testutil.ToFloat64(m.OldestIncidentAgeSeconds.WithLabelValues(string(opsv1alpha1.PhaseDetected), "critical"))
	if age < 0 {
		t.Errorf("OldestIncidentAge 应为非负, 得到 %v", age)
	}
	// warning 不应成为 oldest(其 phaseStart 更晚)。
	if got := testutil.ToFloat64(m.OldestIncidentAgeSeconds.WithLabelValues(string(opsv1alpha1.PhaseDetected), "warning")); got != 0 {
		t.Errorf("warning 不应成为 Detected oldest, 得到 %v", got)
	}
}

func TestIncidentMetricsReconciler_Recompute_ListError(t *testing.T) {
	// 无 scheme 的 fake client 会让 List 失败,recompute 只记录错误不 panic。
	m, err := observability.NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	r := &IncidentMetricsReconciler{
		Client:  fake.NewClientBuilder().Build(), // 无 scheme,List 失败
		Metrics: m,
		Logger:  logr.Discard(),
	}
	r.recompute(context.Background())
}

func TestIncidentMetricsReconciler_Start(t *testing.T) {
	m, err := observability.NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	r := &IncidentMetricsReconciler{
		Client:   metricsClient(t),
		Metrics:  m,
		Logger:   logr.Discard(),
		Interval: 2 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func TestIncidentMetricsReconciler_Start_DefaultInterval(t *testing.T) {
	m, err := observability.NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	r := &IncidentMetricsReconciler{
		Client:  metricsClient(t),
		Metrics: m,
		Logger:  logr.Discard(),
		// Interval 留空,应回退 30s 且立即 recompute 一次后随 ctx 取消退出。
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Start(ctx) }()
	// 等待首个 recompute 触发(立即调用),随后取消。
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start 未在取消后退出")
	}
}
