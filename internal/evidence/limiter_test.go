package evidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLimitItems_UnderLimit(t *testing.T) {
	items := []EvidenceItem{
		{ID: "a", Summary: strings.Repeat("x", 100)},
		{ID: "b", Summary: strings.Repeat("y", 100)},
	}
	out, report := LimitItems(items, 1024)
	if len(out) != 2 || report.Truncated || report.Dropped != 0 {
		t.Errorf("未超限不应截断: %+v", report)
	}
}

func TestLimitItems_OverLimit(t *testing.T) {
	items := []EvidenceItem{
		{ID: "a", Summary: strings.Repeat("x", 1000)},
		{ID: "b", Summary: strings.Repeat("y", 1000)},
	}
	out, report := LimitItems(items, 1500)
	if !report.Truncated || report.Dropped != 1 {
		t.Errorf("应丢弃 1 条: %+v", report)
	}
	if len(out) != 1 || out[0].ID != "a" {
		t.Errorf("应保留最早的条目: %+v", out)
	}
	if report.OriginalBytes <= report.FinalBytes {
		t.Errorf("字节统计错误: %d vs %d", report.OriginalBytes, report.FinalBytes)
	}
}

func TestLimitItems_Deterministic(t *testing.T) {
	items := []EvidenceItem{
		{ID: "a", Summary: strings.Repeat("x", 500)},
		{ID: "b", Summary: strings.Repeat("y", 500)},
		{ID: "c", Summary: strings.Repeat("z", 500)},
	}
	out1, _ := LimitItems(items, 1200)
	out2, _ := LimitItems(items, 1200)
	if len(out1) != len(out2) {
		t.Error("限流结果不稳定")
	}
	for idx := range out1 {
		if out1[idx].ID != out2[idx].ID {
			t.Error("限流顺序不稳定")
		}
	}
}

func TestItemSize_Serializes(t *testing.T) {
	item := EvidenceItem{ID: "x", Kind: KindAlert, Summary: "hello"}
	size := itemSize(item)
	if size <= 0 {
		t.Error("序列化大小无效")
	}
	_, _ = json.Marshal(item)
}
