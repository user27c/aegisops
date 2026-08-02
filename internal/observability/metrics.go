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

	// ---- M9.3 新增(低基数,固定枚举 label) ----
	// ActiveIncidents 是当前活跃事故数,label: phase,severity(由 reconciler 整体重算)。
	ActiveIncidents *prometheus.GaugeVec
	// OldestIncidentAgeSeconds 是各 phase 最老事故年龄,label: phase,severity。
	OldestIncidentAgeSeconds *prometheus.GaugeVec
	// PhaseTransitions 是状态转移总数,label: from,to。
	PhaseTransitions *prometheus.CounterVec
	// EvidenceCollections 是证据采集总数,label: source,result。
	EvidenceCollections *prometheus.CounterVec
	// EvidenceItems 是单次采集条目数,label: source。
	EvidenceItems *prometheus.HistogramVec
	// AuditWrites 是审计写入总数,label: severity,result。
	AuditWrites *prometheus.CounterVec
	// TargetLockAcquire 是目标锁获取总数,label: result。
	TargetLockAcquire *prometheus.CounterVec
	// TargetLockContention 是目标锁竞争总数。
	TargetLockContention prometheus.Counter
	// NotificationHints 是通知提示总数,label: kind(不负责真正发邮件)。
	NotificationHints *prometheus.CounterVec
}

// ResetActiveIncidents 清空活跃事故 gauge(重算前调用)。
func (m *Metrics) ResetActiveIncidents() {
	m.ActiveIncidents.Reset()
	m.OldestIncidentAgeSeconds.Reset()
}

// ObserveActiveIncident 累加一个活跃事故。
func (m *Metrics) ObserveActiveIncident(phase, severity string, count float64) {
	m.ActiveIncidents.WithLabelValues(phase, severity).Add(count)
}

// ObserveOldestIncidentAge 设置某 phase 最老事故年龄。
func (m *Metrics) ObserveOldestIncidentAge(phase, severity string, ageSeconds float64) {
	m.OldestIncidentAgeSeconds.WithLabelValues(phase, severity).Set(ageSeconds)
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
