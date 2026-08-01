package evidence

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

func testIncident() *opsv1alpha1.AIOpsIncident {
	return &opsv1alpha1.AIOpsIncident{
		Spec: opsv1alpha1.AIOpsIncidentSpec{
			TargetRef: opsv1alpha1.TargetReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Namespace:  "fault-lab",
				Name:       "checkout-api",
			},
			StartedAt: metav1.NewTime(time.Now().Add(-30 * time.Minute)),
		},
	}
}

func TestQueriesForIncident_ReturnsRegistered(t *testing.T) {
	specs, err := QueriesForIncident(testIncident())
	if err != nil {
		t.Fatalf("QueriesForIncident: %v", err)
	}
	// 蓝图 11.8 固定 8 个查询。
	if len(specs) != 8 {
		t.Errorf("应有 8 个查询，得到 %d", len(specs))
	}
	expected := []string{
		"container_memory_working_set", "container_memory_limit", "container_cpu_usage",
		"container_cpu_throttled_ratio", "workload_ready_replicas", "http_error_ratio",
		"http_latency_p95", "container_restarts_delta",
	}
	for idx, id := range expected {
		if specs[idx].ID != id {
			t.Errorf("查询顺序/ID 错误: %s != %s", specs[idx].ID, id)
		}
	}
}

func TestRenderQuery_EscapesLabels(t *testing.T) {
	spec := queryTemplates[0]
	query, err := RenderQuery(spec, SafeLabels{Namespace: "fault.lab", Workload: "checkout+api"})
	if err != nil {
		t.Fatalf("RenderQuery: %v", err)
	}
	// 特殊字符必须被 regex escape。
	if !strings.Contains(query, `fault\.lab`) {
		t.Errorf("namespace 未转义: %s", query)
	}
	if strings.Contains(query, "checkout+api") {
		t.Errorf("workload 未转义: %s", query)
	}
}

func TestRenderQuery_MissingParams(t *testing.T) {
	if _, err := RenderQuery(queryTemplates[0], SafeLabels{}); err == nil {
		t.Error("空参数应报错")
	}
}

func TestBuildSafeLogQL(t *testing.T) {
	q := BuildSafeLogQL("fault-lab", `checkout-api-.+`)
	if !strings.Contains(q, `namespace="fault-lab"`) {
		t.Errorf("LogQL 错误: %s", q)
	}
	if !strings.Contains(q, `pod=~"checkout-api-.+"`) {
		t.Errorf("LogQL pod selector 错误: %s", q)
	}
}

func TestPodSelectorFor(t *testing.T) {
	sel, err := podSelectorFor(testIncident())
	if err != nil {
		t.Fatalf("podSelectorFor: %v", err)
	}
	if sel != `checkout-api-.+` {
		t.Errorf("selector 错误: %s", sel)
	}
}
