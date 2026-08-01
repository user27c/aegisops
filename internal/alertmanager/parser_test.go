package alertmanager

import (
	"strings"
	"testing"
	"time"
)

func TestDecodeWebhook(t *testing.T) {
	body := `{"version":"4","groupKey":"{}:{alertname=\"CPU\"}","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"CPU","namespace":"fault-lab","workload":"checkout"},"startsAt":"2026-08-01T10:00:00Z","fingerprint":"abc123"}]}`
	hook, err := DecodeWebhook(strings.NewReader(body), 1<<20)
	if err != nil {
		t.Fatalf("DecodeWebhook 失败: %v", err)
	}
	if len(hook.Alerts) != 1 {
		t.Fatalf("期望 1 条告警，得到 %d", len(hook.Alerts))
	}
	if hook.Alerts[0].Labels["alertname"] != "CPU" {
		t.Errorf("alertname 解析错误: %v", hook.Alerts[0].Labels)
	}
}

func TestDecodeWebhook_OverLimit(t *testing.T) {
	body := `{"alerts":[{"labels":{"alertname":"A"}}]}`
	_, err := DecodeWebhook(strings.NewReader(body), 10)
	if err == nil {
		t.Fatal("超过字节限制应报错")
	}
}

func TestDecodeWebhook_InvalidJSON(t *testing.T) {
	_, err := DecodeWebhook(strings.NewReader("{not-json"), 1<<20)
	if err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

func TestNormalizeAlert_OK(t *testing.T) {
	alert := Alert{
		Status: "firing",
		Labels: map[string]string{
			"alertname": "ContainerOOMKilled", "namespace": "fault-lab",
			"deployment": "checkout", "severity": "critical",
			"kubernetes_pod_name": "checkout-abc-123", // 非白名单键应被丢弃
		},
		Annotations: map[string]string{
			"summary": "OOM", "secret": "should-not-leak",
		},
		StartsAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
	}
	na, err := NormalizeAlert("local-k3s", "{}:{}", alert)
	if err != nil {
		t.Fatalf("NormalizeAlert 失败: %v", err)
	}
	if na.Target.Name != "checkout" || na.Target.Namespace != "fault-lab" {
		t.Errorf("目标解析错误: %+v", na.Target)
	}
	if _, ok := na.Labels["kubernetes_pod_name"]; ok {
		t.Error("非白名单标签不应保留")
	}
	if _, ok := na.Annotations["secret"]; ok {
		t.Error("非白名单注释不应保留")
	}
	if na.Severity != "critical" {
		t.Errorf("severity 错误: %s", na.Severity)
	}
}

func TestNormalizeAlert_MissingLabels(t *testing.T) {
	cases := []map[string]string{
		{},                                   // 无任何标签
		{"alertname": "X"},                   // 缺 namespace
		{"alertname": "X", "namespace": "n"}, // 缺 workload/deployment
	}
	for _, labels := range cases {
		alert := Alert{Status: "firing", Labels: labels}
		if _, err := NormalizeAlert("c", "g", alert); err == nil {
			t.Errorf("标签 %v 应报错", labels)
		}
	}
}

func TestNormalizeAlert_BadStatus(t *testing.T) {
	alert := Alert{Status: "unknown", Labels: map[string]string{"alertname": "X", "namespace": "n", "workload": "w"}}
	if _, err := NormalizeAlert("c", "g", alert); err == nil {
		t.Error("非法状态应报错")
	}
}

func TestNormalizeAlert_Truncation(t *testing.T) {
	alert := Alert{
		Status: "firing",
		Labels: map[string]string{"alertname": "X", "namespace": "n", "workload": "w"},
		Annotations: map[string]string{
			"description": strings.Repeat("密", 2000), // 中文 UTF-8,应被截断且不破坏序列
		},
	}
	na, err := NormalizeAlert("c", "g", alert)
	if err != nil {
		t.Fatalf("NormalizeAlert 失败: %v", err)
	}
	desc := na.Annotations["description"]
	if len(desc) > maxAnnotationValueLength {
		t.Errorf("注释未截断: %d bytes", len(desc))
	}
	if !strings.HasSuffix(desc, "密") {
		t.Errorf("UTF-8 截断破坏字符: %q", desc[len(desc)-3:])
	}
}

func TestSanitizeMetadata(t *testing.T) {
	in := map[string]string{"a": "1", "b": "2", "c": "3"}
	out := SanitizeMetadata(in, map[string]bool{"a": true, "b": true}, 1, 100)
	if len(out) != 1 {
		t.Errorf("数量限制失效: %d", len(out))
	}
	if _, ok := out["c"]; ok {
		t.Error("白名单外键不应出现")
	}
}
