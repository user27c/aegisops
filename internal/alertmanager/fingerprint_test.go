package alertmanager

import (
	"strings"
	"testing"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

func sampleAlert(name string) NormalizedAlert {
	return NormalizedAlert{
		Cluster:             "cluster-a",
		Status:              StatusFiring,
		AlertName:           name,
		Severity:            "critical",
		Target:              opsv1alpha1.TargetReference{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "fault-lab", Name: "checkout", UID: "u-1"},
		StartsAt:            time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		UpstreamFingerprint: "upstream-fp-1",
	}
}

func TestBuildFingerprint_StableAndDistinct(t *testing.T) {
	a1 := sampleAlert("ContainerOOMKilled")
	a2 := sampleAlert("ContainerOOMKilled")
	f1 := BuildFingerprint(a1)
	f2 := BuildFingerprint(a2)
	if f1 != f2 {
		t.Error("相同输入指纹应一致")
	}

	// 不同集群不冲突。
	a3 := sampleAlert("ContainerOOMKilled")
	a3.Cluster = "cluster-b"
	if BuildFingerprint(a3) == f1 {
		t.Error("不同集群指纹不应相同")
	}

	// 不同目标不冲突。
	a4 := sampleAlert("ContainerOOMKilled")
	a4.Target.Name = "other"
	if BuildFingerprint(a4) == f1 {
		t.Error("不同目标指纹不应相同")
	}

	// 不同告警名不冲突。
	a5 := sampleAlert("ContainerCrashLooping")
	if BuildFingerprint(a5) == f1 {
		t.Error("不同告警名指纹不应相同")
	}

	// 不同上游指纹不冲突。
	a6 := sampleAlert("ContainerOOMKilled")
	a6.UpstreamFingerprint = "upstream-fp-2"
	if BuildFingerprint(a6) == f1 {
		t.Error("不同上游指纹不应相同")
	}
}

func TestCanonicalLabels_OrderIndependent(t *testing.T) {
	keys := []string{"alertname", "namespace", "workload"}
	labels1 := map[string]string{"workload": "w", "alertname": "A", "namespace": "n"}
	labels2 := map[string]string{"namespace": "n", "workload": "w", "alertname": "A"}
	if string(CanonicalLabels(labels1, keys)) != string(CanonicalLabels(labels2, keys)) {
		t.Error("map 顺序不应影响编码结果")
	}
	if !strings.Contains(string(CanonicalLabels(labels1, keys)), "alertname=A") {
		t.Error("编码应包含全部键值")
	}
}

func TestIncidentName(t *testing.T) {
	name := IncidentName("ContainerOOMKilled", "sha256:abcdef0123456789")
	if !strings.HasPrefix(name, "containeroomkilled-") {
		t.Errorf("名称前缀错误: %s", name)
	}
	if len(name) > 63 {
		t.Errorf("名称超长: %d", len(name))
	}
	// DNS-1123 兼容。
	for _, r := range name {
		if r < 'a' || r > 'z' {
			if r < '0' || r > '9' {
				if r != '-' {
					t.Errorf("名称含非法字符 %q", r)
				}
			}
		}
	}

	// 空名称兜底。
	empty := IncidentName("!!!", "sha256:abcdef0123456789")
	if !strings.HasPrefix(empty, "incident-") {
		t.Errorf("空告警名兜底失败: %s", empty)
	}

	// 超长截断。
	long := IncidentName(strings.Repeat("x", 100), strings.Repeat("y", 100))
	if len(long) > 63 {
		t.Errorf("超长名称未截断: %d", len(long))
	}
}
