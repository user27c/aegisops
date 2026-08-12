package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/evidence"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const e2eLokiServiceURL = "http://loki.observability.svc:3100"

// TestE2ELokiEvidenceInPack 在真实 Kind 集群验证 Loki 取证闭环:
//  1. operator 已通过 LOKI_URL 注入集群内 Loki;
//  2. Promtail 已把 faultlab 真实 stdout 日志送入 Loki;
//  3. 生产 MultiCollector(真实 K8s/Prometheus/Loki)生成的 EvidencePack
//     包含 Source=loki 的 LogExcerpt,且内容含唯一 marker。
func TestE2ELokiEvidenceInPack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	e := testEnv(t)
	if e.Profile != "full" || e.LokiURL == "" {
		t.Skip("Loki 仅在 full E2E profile 安装")
	}

	assertOperatorLOKIURL(ctx, t, e)

	// 先证明真实日志链路:Promtail 必须已经采集到 faultlab 的 stdout 日志。
	if err := waitLokiRealFaultlabLog(ctx, e, 2*time.Minute); err != nil {
		t.Fatalf("Promtail→Loki 真实日志链路未就绪: %v", err)
	}

	marker := fmt.Sprintf("loki-e2e-%d", time.Now().UnixNano())
	if err := pushLokiMarker(ctx, e, marker); err != nil {
		t.Fatalf("向 Loki push marker: %v", err)
	}
	if err := waitLokiMarker(ctx, e, marker, 30*time.Second); err != nil {
		t.Fatalf("Loki 未查询到 marker: %v", err)
	}

	inc, err := newEvidenceTestIncident(ctx, e)
	if err != nil {
		t.Fatalf("构造测试 incident: %v", err)
	}
	pack := collectWithLoki(ctx, t, e, inc, e.LokiURL)
	if pack.Partial {
		t.Fatalf("Loki 可用时证据包不应 partial: %+v", pack.MissingSources)
	}
	item := findLokiItem(pack.Items, marker)
	if item == nil {
		t.Fatalf("EvidencePack 缺少含 marker 的 Loki LogExcerpt(items=%d)", len(pack.Items))
	}
	// 脱敏断言:pushLokiMarker 在 marker 行内嵌 password=test-secret-abc123,
	// 证据链路必须用 redactor 剥离该值,绝不能原样进入 EvidencePack。
	if strings.Contains(item.Summary, "test-secret-abc123") {
		t.Fatalf("Loki 证据未脱敏,password 值泄漏: %q", item.Summary)
	}
	if !strings.Contains(item.Summary, "private-key-field-REDACTED") {
		t.Fatalf("Loki 证据未按预期脱敏 password 字段: %q", item.Summary)
	}
	t.Logf("EvidencePack 含 Loki LogLine: id=%s kind=%s source=%s summary=%q",
		item.ID, item.Kind, item.Source, item.Summary)
}

// TestE2ELokiFailureStaysPartial 验证可选源 fail-safe 语义:LOKI_URL 指向不可达
// 地址时,EvidencePack 仍由必需源 K8s 与可选源 Prometheus 产出,只标记 partial,
// 绝不因 Loki 故障整体 fail-closed。
func TestE2ELokiFailureStaysPartial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e := testEnv(t)
	if e.Profile != "full" || e.LokiURL == "" {
		t.Skip("Loki 仅在 full E2E profile 安装")
	}

	inc, err := newEvidenceTestIncident(ctx, e)
	if err != nil {
		t.Fatalf("构造测试 incident: %v", err)
	}
	pack := collectWithLoki(ctx, t, e, inc, "http://127.0.0.1:1")
	if !pack.Partial {
		t.Fatalf("Loki 故障时必须标记 partial")
	}
	if !containsString(pack.MissingSources, "loki") {
		t.Fatalf("MissingSources 应包含 loki,实际 %+v", pack.MissingSources)
	}
	k8sCount := 0
	for _, item := range pack.Items {
		if item.Source == "loki" {
			t.Fatalf("Loki 故障时不应出现 loki 证据条目")
		}
		if !strings.HasPrefix(item.Source, "prometheus/") {
			k8sCount++
		}
	}
	if k8sCount == 0 {
		t.Fatalf("Loki 故障时 K8s 必需证据缺失")
	}
	t.Logf("Loki 故障保持 partial: MissingSources=%v, K8s items=%d, Prom items=%d",
		pack.MissingSources, k8sCount, len(pack.Items)-k8sCount)
}

func assertOperatorLOKIURL(ctx context.Context, t *testing.T, e *Environment) {
	t.Helper()
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: e.SystemNamespace, Name: "aegisops-operator"}
	if err := e.K8s.Get(ctx, key, &d); err != nil {
		t.Fatalf("读取 operator Deployment: %v", err)
	}
	for _, c := range d.Spec.Template.Spec.Containers {
		if c.Name != "operator" {
			continue
		}
		for _, env := range c.Env {
			if env.Name == "LOKI_URL" {
				if env.Value != e2eLokiServiceURL {
					t.Fatalf("operator LOKI_URL=%q,期望 %q", env.Value, e2eLokiServiceURL)
				}
				return
			}
		}
	}
	t.Fatal("operator Deployment 缺少 LOKI_URL env")
}

func newEvidenceTestIncident(ctx context.Context, e *Environment) (*opsv1alpha1.AIOpsIncident, error) {
	var dep appsv1.Deployment
	key := types.NamespacedName{Namespace: e.Namespace, Name: "faultlab"}
	if err := e.K8s.Get(ctx, key, &dep); err != nil {
		return nil, err
	}
	now := metav1.Now()
	return &opsv1alpha1.AIOpsIncident{
		ObjectMeta: metav1.ObjectMeta{Name: "loki-evidence-integration", Namespace: e.Namespace},
		Spec: opsv1alpha1.AIOpsIncidentSpec{
			Fingerprint:  strings.Repeat("a", 64),
			Cluster:      "kind-e2e",
			AlertName:    "CheckoutHTTP500s",
			Severity:     "critical",
			SourceStatus: "firing",
			TargetRef: opsv1alpha1.TargetReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Namespace:  e.Namespace,
				Name:       "faultlab",
				UID:        dep.UID,
			},
			StartedAt:      now,
			LastReceivedAt: now,
		},
	}, nil
}

func collectWithLoki(ctx context.Context, t *testing.T, e *Environment, inc *opsv1alpha1.AIOpsIncident, lokiURL string) evidence.EvidencePack {
	t.Helper()
	prom, err := evidence.NewHTTPPromClient(e.PrometheusURL, &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建 Prometheus 客户端: %v", err)
	}
	loki, err := evidence.NewHTTPLokiClient(lokiURL, &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建 Loki 客户端: %v", err)
	}
	collector := &evidence.MultiCollector{
		K8s:    &evidence.KubernetesCollector{Client: e.K8s},
		Prom:   prom,
		Loki:   loki,
		Redact: evidence.NewRegexRedactor(nil),
		Limits: evidence.DefaultLimits(),
		Now:    time.Now,
	}
	pack, err := collector.Collect(ctx, inc)
	if err != nil {
		t.Fatalf("MultiCollector.Collect: %v", err)
	}
	return pack
}

func findLokiItem(items []evidence.EvidenceItem, marker string) *evidence.EvidenceItem {
	for i := range items {
		item := &items[i]
		if item.Kind == evidence.KindLogExcerpt && item.Source == "loki" &&
			strings.Contains(item.Summary, marker) {
			return item
		}
	}
	return nil
}

func pushLokiMarker(ctx context.Context, e *Environment, marker string) error {
	payload := map[string]any{
		"streams": []map[string]any{{
			"stream": map[string]string{
				"namespace": e.Namespace,
				"pod":       "faultlab-evidence-" + marker,
				"app":       "evidence-check",
			},
			"values": [][]string{{
				fmt.Sprintf("%d", time.Now().UnixNano()),
				fmt.Sprintf(`level=error msg="checkout request failed" marker=%s password=test-secret-abc123`, marker),
			}},
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.LokiURL+"/loki/api/v1/push", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusOK {
		return fmt.Errorf("Loki push 返回 %d", res.StatusCode)
	}
	return nil
}

func waitLokiRealFaultlabLog(ctx context.Context, e *Environment, timeout time.Duration) error {
	query := fmt.Sprintf(`{namespace=%q, app="faultlab"}`, e.Namespace)
	return waitFor(ctx, timeout, func() (bool, string) {
		n, err := queryLoki(ctx, e, query)
		if err != nil {
			return false, err.Error()
		}
		return n > 0, fmt.Sprintf("Loki 中 faultlab 日志行数=%d", n)
	})
}

func waitLokiMarker(ctx context.Context, e *Environment, marker string, timeout time.Duration) error {
	query := fmt.Sprintf(`{namespace=%q, pod=~"faultlab-.+"} |= %q`, e.Namespace, marker)
	return waitFor(ctx, timeout, func() (bool, string) {
		n, err := queryLoki(ctx, e, query)
		if err != nil {
			return false, err.Error()
		}
		return n > 0, fmt.Sprintf("marker 命中流数=%d", n)
	})
}

type lokiQueryResult struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Values [][]string `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func queryLoki(ctx context.Context, e *Environment, query string) (int, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("limit", "100")
	q.Set("start", fmt.Sprintf("%d", time.Now().Add(-2*time.Hour).UnixNano()))
	q.Set("end", fmt.Sprintf("%d", time.Now().UnixNano()))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.LokiURL+"/loki/api/v1/query_range?"+q.Encode(), nil)
	if err != nil {
		return 0, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Loki query HTTP %d", res.StatusCode)
	}
	var out lokiQueryResult
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return 0, err
	}
	if out.Status != "success" {
		return 0, fmt.Errorf("Loki query status=%s", out.Status)
	}
	count := 0
	for _, stream := range out.Data.Result {
		count += len(stream.Values)
	}
	return count, nil
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
