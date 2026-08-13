package verifier

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

type fakeProm struct {
	raw     json.RawMessage
	err     error
	lastQry string
}

func (f *fakeProm) Query(_ context.Context, q string, _ time.Time) (json.RawMessage, error) {
	f.lastQry = q
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
			prom := tc.prom
			got, err := (&PrometheusScaleMetrics{Prom: &prom, MaxThrottledRatio: 0.1}).CheckScale(context.Background(), scaleIncident())
			if err != nil || got.Healthy != tc.want {
				t.Fatalf("healthy=%v err=%v want=%v", got.Healthy, err, tc.want)
			}
		})
	}
}

// TestPrometheusScaleMetrics_QueryFiltersPodLevelAggregate 断言查询过滤掉
// cAdvisor 的 pod 级聚合序列,避免 sum by (pod) 把 pod 级与 container 级
// 的限流比例重复相加得到 >1 的越界值。
func TestPrometheusScaleMetrics_QueryFiltersPodLevelAggregate(t *testing.T) {
	prom := &fakeProm{raw: json.RawMessage(`{"resultType":"vector","result":[{"value":[1,"0.05"]}]}`)}
	_, err := (&PrometheusScaleMetrics{Prom: prom, MaxThrottledRatio: 0.1}).CheckScale(context.Background(), scaleIncident())
	if err != nil {
		t.Fatalf("CheckScale: %v", err)
	}
	if !strings.Contains(prom.lastQry, `container!=""`) {
		t.Fatalf("查询应包含 container!=\"\" 过滤 pod 级聚合序列: %s", prom.lastQry)
	}
}

func TestPrometheusScaleMetrics_RequiresPrometheus(t *testing.T) {
	got, err := (&PrometheusScaleMetrics{MaxThrottledRatio: 0.1}).CheckScale(context.Background(), scaleIncident())
	if err != nil || got.Healthy {
		t.Fatalf("missing Prometheus must fail closed: %+v %v", got, err)
	}
}
