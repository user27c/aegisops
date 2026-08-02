package policy

import (
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestValidateRestart(t *testing.T) {
	// 理由过短。
	if err := ValidateRestart(RestartParams{Reason: "ab"}, RestartConstraints{RequireReason: true}); err == nil {
		t.Error("短理由应报错")
	}
	// 理由超长。
	if err := ValidateRestart(RestartParams{Reason: strings.Repeat("x", 300)}, RestartConstraints{}); err == nil {
		t.Error("超长理由应报错")
	}
	// 合法。
	if err := ValidateRestart(RestartParams{Reason: "CrashLoopBackOff 持续"}, RestartConstraints{RequireReason: true}); err != nil {
		t.Errorf("合法理由应通过: %v", err)
	}
}

func TestValidateScale(t *testing.T) {
	c := ScaleConstraints{MaxReplicas: 8, MaxReplicaDelta: 2}
	if err := ValidateScale(2, ScaleParams{Replicas: 10}, c); err == nil {
		t.Error("超 maxReplicas 应报错")
	}
	if err := ValidateScale(2, ScaleParams{Replicas: 5}, c); err == nil {
		t.Error("delta 超限应报错")
	}
	if err := ValidateScale(2, ScaleParams{Replicas: 0}, c); err == nil {
		t.Error("replicas<=0 应报错")
	}
	if err := ValidateScale(2, ScaleParams{Replicas: 4}, c); err != nil {
		t.Errorf("合法扩容应通过: %v", err)
	}
	// HPA 场景。
	if err := ValidateScale(2, ScaleParams{Replicas: 4}, ScaleConstraints{RespectHPA: true}); err == nil {
		t.Error("HPA 场景应拒绝")
	}
	// 缩容也受 delta 限制。
	if err := ValidateScale(5, ScaleParams{Replicas: 2}, ScaleConstraints{MaxReplicaDelta: 2}); err == nil {
		t.Error("缩容 delta 超限应报错")
	}
}

func TestValidateResourcePatch(t *testing.T) {
	maxMem := resource.MustParse("1Gi")
	c := ResourceConstraints{MaxMemory: &maxMem, MaxIncreasePercent: 200}
	current := map[string]resource.Quantity{"memory": resource.MustParse("256Mi")}

	if err := ValidateResourcePatch(current, ResourcePatchParams{Container: "", MemoryLimit: "512Mi"}, c); err == nil {
		t.Error("缺 container 应报错")
	}
	if err := ValidateResourcePatch(current, ResourcePatchParams{Container: "app"}, c); err == nil {
		t.Error("无 limit 应报错")
	}
	if err := ValidateResourcePatch(current, ResourcePatchParams{Container: "app", MemoryLimit: "2Gi"}, c); err == nil {
		t.Error("超上限应报错")
	}
	if err := ValidateResourcePatch(current, ResourcePatchParams{Container: "app", MemoryLimit: "1Gi"}, c); err == nil {
		t.Error("增幅 300% 超 200% 应报错")
	}
	if err := ValidateResourcePatch(current, ResourcePatchParams{Container: "app", MemoryLimit: "512Mi"}, c); err != nil {
		t.Errorf("合法调整应通过: %v", err)
	}
	// 非法数量。
	if err := ValidateResourcePatch(current, ResourcePatchParams{Container: "app", MemoryLimit: "abc"}, c); err == nil {
		t.Error("非法数量应报错")
	}
	// 负值。
	if err := ValidateResourcePatch(current, ResourcePatchParams{Container: "app", MemoryLimit: "-1Gi"}, c); err == nil {
		t.Error("负 limit 应报错")
	}
	// CPU 校验。
	maxCPU := resource.MustParse("2")
	cpuC := ResourceConstraints{MaxCPU: &maxCPU}
	if err := ValidateResourcePatch(nil, ResourcePatchParams{Container: "app", CPULimit: "4"}, cpuC); err == nil {
		t.Error("CPU 超上限应报错")
	}
	if err := ValidateResourcePatch(nil, ResourcePatchParams{Container: "app", CPULimit: "1"}, cpuC); err != nil {
		t.Errorf("合法 CPU 应通过: %v", err)
	}
	// AllowLimitRemoval 永远拒绝。
	if err := ValidateResourcePatch(nil, ResourcePatchParams{Container: "app", MemoryLimit: "512Mi"}, ResourceConstraints{AllowLimitRemoval: true}); err == nil {
		t.Error("禁止移除 limit 的语义被破坏")
	}
}

func TestValidateRollback(t *testing.T) {
	c := RollbackConstraints{MaxRevisionDistance: 3}
	if err := ValidateRollback(5, RollbackParams{TargetRevision: 0}, c); err == nil {
		t.Error("targetRevision<=0 应报错")
	}
	if err := ValidateRollback(5, RollbackParams{TargetRevision: 6}, c); err == nil {
		t.Error("targetRevision>=current 应报错")
	}
	if err := ValidateRollback(5, RollbackParams{TargetRevision: 1}, c); err == nil {
		t.Error("回滚距离超限应报错")
	}
	if err := ValidateRollback(5, RollbackParams{TargetRevision: 3}, c); err != nil {
		t.Errorf("合法回滚应通过: %v", err)
	}
}

func TestValidateConfigRestore(t *testing.T) {
	c := ConfigMapConstraints{AllowedNames: []string{"checkout-config"}, RequireImmutableBackup: true}
	if err := ValidateConfigRestore(RestoreConfigMapParams{TargetConfigMap: "", BackupConfigMap: "b"}, c); err == nil {
		t.Error("缺目标应报错")
	}
	if err := ValidateConfigRestore(RestoreConfigMapParams{TargetConfigMap: "a", BackupConfigMap: "a"}, c); err == nil {
		t.Error("同名应报错")
	}
	if err := ValidateConfigRestore(RestoreConfigMapParams{TargetConfigMap: "other", BackupConfigMap: "b"}, c); err == nil {
		t.Error("白名单外应报错")
	}
	if err := ValidateConfigRestore(RestoreConfigMapParams{TargetConfigMap: "checkout-config", BackupConfigMap: "b"}, c); err != nil {
		t.Errorf("合法恢复应通过: %v", err)
	}
}

func TestParseParameters(t *testing.T) {
	p := params(t, map[string]any{"replicas": float64(3)})
	out, err := ParseParameters(p)
	if err != nil || out["replicas"] != float64(3) {
		t.Errorf("解析失败: %v %v", out, err)
	}
	// 非法 JSON。
	bad := apiextensionsv1.JSON{Raw: []byte("{bad")}
	if _, err := ParseParameters(bad); err == nil {
		t.Error("非法 JSON 应报错")
	}
	// 空参数。
	empty, err := ParseParameters(apiextensionsv1.JSON{})
	if err != nil || len(empty) != 0 {
		t.Errorf("空参数应返回空 map: %v %v", empty, err)
	}
}
