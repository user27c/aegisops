package policy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCanonicalJSON_NestedAndNumbers(t *testing.T) {
	in := map[string]any{
		"a": map[string]any{"z": json.Number("1.5"), "b": []any{"x", float64(2)}},
		"c": nil,
	}
	raw, err := CanonicalJSON(in)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if !strings.Contains(string(raw), `"a"`) || !strings.Contains(string(raw), `"c":null`) {
		t.Errorf("结果异常: %s", raw)
	}
	// 结构体输入也能处理。
	raw2, err := CanonicalJSON(struct{ A string }{A: "x"})
	if err != nil || !strings.Contains(string(raw2), `"A":"x"`) {
		t.Errorf("结构体序列化失败: %s %v", raw2, err)
	}
	// 非法输入(循环引用)应报错——用 chan 模拟不可序列化。
	if _, err := CanonicalJSON(map[string]any{"c": make(chan int)}); err == nil {
		t.Error("不可序列化值应报错")
	}
}

func TestSortJSONKeys(t *testing.T) {
	raw := []byte(`{"b":1,"a":{"d":2,"c":3}}`)
	sorted, err := SortJSONKeys(raw)
	if err != nil {
		t.Fatalf("SortJSONKeys: %v", err)
	}
	if !strings.Contains(string(sorted), `"a":{"c":3,"d":2}`) {
		t.Errorf("key 未排序: %s", sorted)
	}
	// 非法 JSON。
	if _, err := SortJSONKeys([]byte("{bad")); err == nil {
		t.Error("非法 JSON 应报错")
	}
}

func TestDecisionHelpers(t *testing.T) {
	d := Decision{Type: DecisionDeny}
	if !d.Denied() || d.Approved() {
		t.Error("Deny 判定错误")
	}
	d = Decision{Type: DecisionAuto}
	if !d.Approved() || d.Denied() {
		t.Error("Auto 判定错误")
	}
}

func TestIntrinsicRisk(t *testing.T) {
	risk, ok := IntrinsicRisk(opsv1alpha1.ActionRestartWorkload)
	if !ok || risk != opsv1alpha1.RiskLow {
		t.Errorf("RestartWorkload 风险错误: %s %v", risk, ok)
	}
	if _, ok := IntrinsicRisk("Unknown"); ok {
		t.Error("未知动作不应注册")
	}
}

func TestEffectiveConstraints(t *testing.T) {
	p := restartPolicy()
	p.Spec.VerificationWindow = &metav1.Duration{Duration: 2 * time.Minute}
	p.Spec.ApprovalTTL = &metav1.Duration{Duration: 10 * time.Minute}
	p.Spec.Cooldown = &metav1.Duration{Duration: 5 * time.Minute}
	p.Spec.RequireAudit = boolPtr(false)
	p.Spec.RollbackOnVerificationFailure = boolPtr(false)

	c := effectiveConstraints(p)
	if c.VerificationWindow != 2*time.Minute || c.ApprovalTTL != 10*time.Minute || c.Cooldown != 5*time.Minute {
		t.Errorf("约束解析错误: %+v", c)
	}
	if c.RequireAudit {
		t.Error("RequireAudit 应为 false")
	}
	if c.RollbackOnVerificationFailure {
		t.Error("RollbackOnVerificationFailure 应为 false")
	}
	// 默认值。
	p2 := restartPolicy()
	c2 := effectiveConstraints(p2)
	if !c2.RequireAudit || !c2.RollbackOnVerificationFailure {
		t.Error("默认值错误")
	}
	if c2.MaxAttemptsPerIncident != 2 {
		t.Errorf("MaxAttempts 错误: %d", c2.MaxAttemptsPerIncident)
	}
}

func TestMatches_InvalidSelector(t *testing.T) {
	p := &opsv1alpha1.RemediationPolicy{
		Spec: opsv1alpha1.RemediationPolicySpec{
			TargetSelector: opsv1alpha1.TargetSelector{
				NamespaceLabels: map[string]string{"bad selector !!!": "x"},
			},
		},
	}
	if _, err := Matches(p, nil, nil, "Deployment"); err == nil {
		t.Error("非法 selector 应报错")
	}
}

func TestDerefHelpers(t *testing.T) {
	if derefInt32(nil) != 0 || derefInt32(int32Ptr(5)) != 5 {
		t.Error("derefInt32 错误")
	}
	if derefInt64(nil) != 0 || derefInt64(int64Ptr(7)) != 7 {
		t.Error("derefInt64 错误")
	}
	if derefBool(nil) != true || derefBool(boolPtr(false)) != false {
		t.Error("derefBool 错误")
	}
	if quantityOrNil(nil) != nil {
		t.Error("nil quantity 应为 nil")
	}
	bad := opsv1alpha1.ResourceQuantity("not-a-qty")
	if quantityOrNil(&bad) != nil {
		t.Error("非法 quantity 应为 nil")
	}
	good := opsv1alpha1.ResourceQuantity("512Mi")
	if quantityOrNil(&good) == nil {
		t.Error("合法 quantity 不应为 nil")
	}
	empty := opsv1alpha1.ResourceQuantity("")
	if quantityOrNil(&empty) != nil {
		t.Error("空 quantity 应为 nil")
	}
}

func TestNumOr(t *testing.T) {
	if numOr(float64(3)) != 3 || numOr("x") != 0 {
		t.Error("numOr 错误")
	}
	if strOr("s") != "s" || strOr(3) != "" {
		t.Error("strOr 错误")
	}
}

func TestValidateParams_MissingReason(t *testing.T) {
	// RestartWorkload 缺理由。
	proposal := opsv1alpha1.ActionProposal{
		Revision: 1, Action: opsv1alpha1.ActionRestartWorkload,
		Parameters: params(t, map[string]any{}),
	}
	in := baseInput(proposal)
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() || decision.Reasons[0].Code != ReasonConstraintsViolated {
		t.Errorf("缺理由应拒绝: %+v", decision)
	}
}

func TestValidateParams_BadJSON(t *testing.T) {
	proposal := opsv1alpha1.ActionProposal{
		Revision: 1, Action: opsv1alpha1.ActionRestartWorkload,
		Parameters: apiextensionsv1.JSON{Raw: []byte("{bad")},
	}
	in := baseInput(proposal)
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if !decision.Denied() {
		t.Errorf("非法参数 JSON 应拒绝: %+v", decision)
	}
}

func TestValidateParams_UnknownAction(t *testing.T) {
	proposal := opsv1alpha1.ActionProposal{
		Revision: 1, Action: opsv1alpha1.ActionRestoreConfigMap,
		Parameters: params(t, map[string]any{"targetConfigMap": "c", "backupConfigMap": "b"}),
	}
	in := baseInput(proposal)
	// RestoreConfigMap 未在 restartPolicy 启用 → Disabled。
	decision, _ := (&DefaultEvaluator{}).Evaluate(context.Background(), in)
	if decision.Reasons[0].Code != ReasonActionDisabled {
		t.Errorf("未启用动作应 Disabled: %+v", decision)
	}
}

func boolPtr(b bool) *bool { return &b }
