package verifier

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

type fakeProm struct {
	raw json.RawMessage
	err error
}

func (f fakeProm) Query(context.Context, string, time.Time) (json.RawMessage, error) {
	return f.raw, f.err
}
func (f fakeProm) QueryRange(context.Context, string, time.Time, time.Time, int) (json.RawMessage, error) {
	return nil, errors.New("not used")
}

func scaleIncident() *opsv1alpha1.AIOpsIncident {
	return &opsv1alpha1.AIOpsIncident{Spec: opsv1alpha1.AIOpsIncidentSpec{TargetRef: opsv1alpha1.TargetReference{Namespace: "fault-lab", Name: "faultlab"}}}
}

func TestPrometheusScaleMetrics_FailClosed(t *testing.T) {
	tests := []struct {
		name string
		prom fakeProm
		want bool
	}{
		{"query error", fakeProm{err: errors.New("down")}, false},
		{"empty", fakeProm{raw: json.RawMessage(`{"resultType":"vector","result":[]}`)}, false},
		{"above threshold", fakeProm{raw: json.RawMessage(`{"resultType":"vector","result":[{"value":[1,"0.2"]}]}`)}, false},
		{"recovered", fakeProm{raw: json.RawMessage(`{"resultType":"vector","result":[{"value":[1,"0.05"]}]}`)}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (&PrometheusScaleMetrics{Prom: tc.prom, MaxThrottledRatio: 0.1}).CheckScale(context.Background(), scaleIncident())
			if err != nil || got.Healthy != tc.want {
				t.Fatalf("healthy=%v err=%v want=%v", got.Healthy, err, tc.want)
			}
		})
	}
}

func TestPrometheusScaleMetrics_RequiresPrometheus(t *testing.T) {
	got, err := (&PrometheusScaleMetrics{MaxThrottledRatio: 0.1}).CheckScale(context.Background(), scaleIncident())
	if err != nil || got.Healthy {
		t.Fatalf("missing Prometheus must fail closed: %+v %v", got, err)
	}
}
