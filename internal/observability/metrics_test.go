package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// TestNewMetrics_AllFieldsRegistered 验证全部指标字段非 nil(防止 nil 指针 panic)。
func TestNewMetrics_AllFieldsRegistered(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewMetrics(reg)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	// 全部字段必须初始化。
	checks := []struct {
		name string
		ok   bool
	}{
		{"Incidents", m.Incidents != nil},
		{"PhaseDuration", m.PhaseDuration != nil},
		{"ActiveIncidents", m.ActiveIncidents != nil},
		{"OldestIncidentAgeSeconds", m.OldestIncidentAgeSeconds != nil},
		{"PhaseTransitions", m.PhaseTransitions != nil},
		{"EvidenceCollections", m.EvidenceCollections != nil},
		{"EvidenceItems", m.EvidenceItems != nil},
		{"AuditWrites", m.AuditWrites != nil},
		{"TargetLockAcquire", m.TargetLockAcquire != nil},
		{"TargetLockContention", m.TargetLockContention != nil},
		{"NotificationHints", m.NotificationHints != nil},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("字段未初始化: %s", c.name)
		}
	}
	// 辅助方法不 panic。
	m.ResetActiveIncidents()
	m.ObserveActiveIncident("Detected", "critical", 1)
	m.ObserveOldestIncidentAge("Detected", "critical", 60)
	m.PhaseTransitions.WithLabelValues("Detected", "CollectingEvidence").Inc()
}

// TestNewMetrics_DuplicateRegistrationFailsLoudly 验证共享 registry 重复创建
// 会以明确错误失败(panic 含 duplicate 字样),而不是静默覆盖或返回 nil 指针。
func TestNewMetrics_DuplicateRegistrationFailsLoudly(t *testing.T) {
	reg := prometheus.NewRegistry()
	if _, err := NewMetrics(reg); err != nil {
		t.Fatalf("第一次: %v", err)
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("重复注册应 panic(duplicate collector)")
		}
		msg := r.(error).Error()
		if !containsStr(msg, "duplicate") {
			t.Errorf("panic 信息应含 duplicate: %s", msg)
		}
	}()
	_, _ = NewMetrics(reg)
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
