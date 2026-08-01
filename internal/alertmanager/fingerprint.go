package alertmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// BuildFingerprint 生成稳定去重指纹。
//
// 指纹 = sha256(cluster | namespace | targetName | alertname | upstreamFingerprint)。
// 必须排序 key 后编码，禁止直接 hash Go map。
func BuildFingerprint(a NormalizedAlert) string {
	h := sha256.New()
	for _, part := range []string{
		a.Cluster,
		a.Target.Namespace,
		a.Target.Name,
		a.AlertName,
		a.UpstreamFingerprint,
	} {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte("|"))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// CanonicalLabels 把标签按 key 排序后编码为稳定字节串，用于摘要与审计。
func CanonicalLabels(labels map[string]string, keys []string) []byte {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)

	var b strings.Builder
	for _, k := range sorted {
		v, ok := labels[k]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	return []byte(b.String())
}

// IncidentName 从告警名与指纹生成 DNS-1123 兼容的 Incident 名称。
//
// 规则：alertname 小写化并只保留 [a-z0-9-]，指纹取前 12 位。
// 结果 ≤ 63 字符。
func IncidentName(alertName, fingerprint string) string {
	// 归一化 alertname：转小写、非 [a-z0-9] 转 '-'
	var sb strings.Builder
	for _, r := range strings.ToLower(alertName) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('-')
		}
	}
	name := strings.Trim(sb.String(), "-")
	if name == "" {
		name = "incident"
	}

	shortFP := fingerprint
	if len(shortFP) > 12 {
		shortFP = shortFP[:12]
	}
	shortFP = strings.TrimPrefix(shortFP, "sha256:")

	full := fmt.Sprintf("%s-%s", name, shortFP)
	if len(full) > 63 {
		full = full[:63]
	}
	return strings.TrimSuffix(full, "-")
}
