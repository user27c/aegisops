package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/types"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
)

// DigestInput 是方案摘要输入。
type DigestInput struct {
	IncidentUID           types.UID
	Target                opsv1alpha1.TargetReference
	TargetResourceVersion string
	Action                opsv1alpha1.ActionType
	Parameters            any
	PolicyUID             types.UID
	PolicyGeneration      int64
}

// BuildPlanDigest 计算方案摘要 sha256:<hex>。
// 绑定 Incident UID、目标 resourceVersion 与 Policy generation；
// 任一变化都会使旧审批失效（不可复用）。
func BuildPlanDigest(input DigestInput) (string, error) {
	canonical, err := CanonicalJSON(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// VerifyPlanDigest 校验摘要是否匹配。
func VerifyPlanDigest(expected string, input DigestInput) error {
	actual, err := BuildPlanDigest(input)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("方案摘要不匹配: 期望 %s，实际 %s", shortDigest(expected), shortDigest(actual))
	}
	return nil
}

// CanonicalJSON 稳定序列化：map key 排序、数量统一字符串、无时间戳等非语义字段。
func CanonicalJSON(v any) ([]byte, error) {
	normalized, err := normalize(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func normalize(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			n, err := normalize(val)
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for idx, val := range t {
			n, err := normalize(val)
			if err != nil {
				return nil, err
			}
			out[idx] = n
		}
		return out, nil
	case string, bool, nil, float64:
		return t, nil
	case json.Number:
		return t.String(), nil
	default:
		// 结构体等：先 JSON 再递归（保持确定性需调用方传入 map）。
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		var anyVal any
		if err := json.Unmarshal(raw, &anyVal); err != nil {
			return nil, err
		}
		return normalize(anyVal)
	}
}

// SortJSONKeys 排序 JSON 对象的 key（调试/审计用）。
func SortJSONKeys(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	n, err := normalize(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(n)
}

func shortDigest(d string) string {
	if len(d) <= 16 {
		return d
	}
	return d[:16] + "..."
}
