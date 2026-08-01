package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics 聚合全部业务指标。禁止把 incident UID/name 作为 label（高基数）。
type Metrics struct {
	// Incidents 是事故总数，label: category,outcome。
	Incidents *prometheus.CounterVec
	// PhaseDuration 是各阶段耗时秒数，label: phase。
	PhaseDuration *prometheus.HistogramVec
	// AnalysisLatency 是诊断耗时秒数。
	AnalysisLatency *prometheus.HistogramVec
	// PolicyDecisions 是策略判定总数，label: decision,reason。
	PolicyDecisions *prometheus.CounterVec
	// Remediations 是修复动作总数，label: action,result。
	Remediations *prometheus.CounterVec
	// VerificationChecks 是验证检查总数，label: state。
	VerificationChecks *prometheus.CounterVec
	// MTTR 是修复时长秒数，label: category。
	MTTR *prometheus.HistogramVec
	// ExternalRequests 是外部请求耗时秒数，label: component,result。
	ExternalRequests *prometheus.HistogramVec
	// ReconcileErrors 是 Reconcile 错误总数。
	ReconcileErrors *prometheus.CounterVec
}

// NewMetrics 注册并返回全部指标。
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	f := promauto.With(reg)
	m := &Metrics{
		Incidents: f.NewCounterVec(prometheus.CounterOpts{
			Name: "aegisops_incidents_total",
			Help: "事故总数",
		}, []string{"category", "outcome"}),
		PhaseDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aegisops_incident_phase_duration_seconds",
			Help:    "各阶段耗时",
			Buckets: prometheus.DefBuckets,
		}, []string{"phase"}),
		AnalysisLatency: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aegisops_diagnosis_latency_seconds",
			Help:    "诊断耗时",
			Buckets: prometheus.DefBuckets,
		}, []string{"result"}),
		PolicyDecisions: f.NewCounterVec(prometheus.CounterOpts{
			Name: "aegisops_policy_decisions_total",
			Help: "策略判定总数",
		}, []string{"decision", "reason"}),
		Remediations: f.NewCounterVec(prometheus.CounterOpts{
			Name: "aegisops_remediation_total",
			Help: "修复动作总数",
		}, []string{"action", "result"}),
		VerificationChecks: f.NewCounterVec(prometheus.CounterOpts{
			Name: "aegisops_verification_checks_total",
			Help: "验证检查总数",
		}, []string{"state"}),
		MTTR: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aegisops_mttr_seconds",
			Help:    "修复时长",
			Buckets: []float64{30, 60, 120, 180, 300, 600, 1200, 1800, 3600},
		}, []string{"category"}),
		ExternalRequests: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aegisops_external_requests_seconds",
			Help:    "外部请求耗时",
			Buckets: prometheus.DefBuckets,
		}, []string{"component", "result"}),
		ReconcileErrors: f.NewCounterVec(prometheus.CounterOpts{
			Name: "aegisops_reconcile_errors_total",
			Help: "Reconcile 错误总数",
		}, []string{"phase"}),
	}
	// 提前初始化常见 label 组合，避免指标首次出现时缺标签。
	for _, phase := range []string{"Detected", "CollectingEvidence", "Diagnosing", "PolicyChecking", "AwaitingApproval", "Executing", "Verifying", "RollingBack"} {
		m.PhaseDuration.WithLabelValues(phase)
	}
	return m, nil
}
