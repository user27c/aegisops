package evidence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// fakeK8s 返回固定证据的 K8s 采集器。
type fakeK8s struct{}

func (f *fakeK8s) Collect(_ context.Context, i *opsv1alpha1.AIOpsIncident) ([]EvidenceItem, TargetSnapshot, error) {
	return []EvidenceItem{{ID: "k8s-1", Kind: KindKubernetesEvent, Summary: "event"}}, TargetSnapshot{Name: i.Spec.TargetRef.Name}, nil
}

// fakeProm 可配置成功/失败。
type fakeProm struct {
	fail bool
}

func (f *fakeProm) Query(_ context.Context, _ string, _ time.Time) (json.RawMessage, error) {
	return json.RawMessage(`{"result":[]}`), nil
}
func (f *fakeProm) QueryRange(_ context.Context, _ string, _ time.Time, _ time.Time, _ int) (json.RawMessage, error) {
	if f.fail {
		return nil, errBoom
	}
	return json.RawMessage(`{"resultType":"matrix","result":[]}`), nil
}

// fakeLoki 可配置成功/失败。
type fakeLoki struct {
	fail bool
}

func (f *fakeLoki) QueryRange(_ context.Context, _ string, _ time.Time, _ time.Time, _ int) ([]LogLine, error) {
	if f.fail {
		return nil, errBoom
	}
	return []LogLine{{Timestamp: time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC), Message: "log line"}}, nil
}

var errBoom = &boomError{}

type boomError struct{}

func (b *boomError) Error() string { return "boom" }

func newMultiCollector(k K8sCollector, p PromClient, l LokiClient, redactor Redactor) *MultiCollector {
	return &MultiCollector{
		K8s:    k,
		Prom:   p,
		Loki:   l,
		Redact: redactor,
		Limits: DefaultLimits(),
		Now:    func() time.Time { return time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC) },
	}
}

func TestMultiCollector_AllSources(t *testing.T) {
	incident := testIncident()
	incident.UID = types.UID("uid-1")
	mc := newMultiCollector(&fakeK8s{}, &fakeProm{}, &fakeLoki{}, NewRegexRedactor(nil))

	pack, err := mc.Collect(context.Background(), incident)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pack.Hash == "" {
		t.Error("哈希为空")
	}
	if pack.Partial {
		t.Error("全部源成功不应标记 partial")
	}
	// k8s-1 + 8 个 prom 查询 + 1 条日志。
	if len(pack.Items) < 10 {
		t.Errorf("证据条目不足: %d", len(pack.Items))
	}
}

func TestMultiCollector_RequiredSourceFailure(t *testing.T) {
	incident := testIncident()
	mc := newMultiCollector(&failingK8s{}, &fakeProm{}, &fakeLoki{}, nil)

	if _, err := mc.Collect(context.Background(), incident); err == nil {
		t.Error("必需源失败应整体失败")
	}
}

func TestMultiCollector_OptionalSourcePartial(t *testing.T) {
	incident := testIncident()
	mc := newMultiCollector(&fakeK8s{}, &fakeProm{fail: true}, &fakeLoki{}, nil)

	pack, err := mc.Collect(context.Background(), incident)
	if err != nil {
		t.Fatalf("可选源失败不应整体失败: %v", err)
	}
	if !pack.Partial {
		t.Error("应标记 partial")
	}
	found := false
	for _, m := range pack.MissingSources {
		if m == "prometheus" {
			found = true
		}
	}
	if !found {
		t.Errorf("MissingSources 缺少 prometheus: %+v", pack.MissingSources)
	}
}

func TestMultiCollector_StableHash(t *testing.T) {
	incident := testIncident()
	mc := newMultiCollector(&fakeK8s{}, &fakeProm{}, &fakeLoki{}, NewRegexRedactor(nil))

	p1, err := mc.Collect(context.Background(), incident)
	if err != nil {
		t.Fatalf("第一次: %v", err)
	}
	p2, err := mc.Collect(context.Background(), incident)
	if err != nil {
		t.Fatalf("第二次: %v", err)
	}
	if p1.Hash != p2.Hash {
		t.Error("相同输入哈希应稳定")
	}
}

type failingK8s struct{}

func (f *failingK8s) Collect(context.Context, *opsv1alpha1.AIOpsIncident) ([]EvidenceItem, TargetSnapshot, error) {
	return nil, TargetSnapshot{}, errBoom
}

func TestHashPack_DifferentContent(t *testing.T) {
	p1 := EvidencePack{SchemaVersion: "v1", Items: []EvidenceItem{{ID: "a"}}}
	p2 := EvidencePack{SchemaVersion: "v1", Items: []EvidenceItem{{ID: "b"}}}
	if HashPack(p1) == HashPack(p2) {
		t.Error("不同内容哈希不应相同")
	}
	h1 := HashPack(p1)
	h2 := HashPack(p1)
	if h1 != h2 {
		t.Error("相同内容哈希应一致")
	}
}
