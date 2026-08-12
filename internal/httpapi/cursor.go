package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// listCursor 是不透明分页游标（M9.4 分页过滤修复）。
//
// 语义：
//   - 服务端先按 namespace 分页 List，再对 phase/severity 做服务端过滤；
//   - cursor 携带过滤条件指纹（filterHash），请求条件与 cursor 不一致时返回 400；
//   - 单次请求最大扫描 maxCursorScan 个对象，防止恶意 limit 造成大范围扫描。
type listCursor struct {
	// Version 是游标版本（当前 1），未来格式变化可平滑迁移。
	Version int `json:"v"`
	// Namespace/Phase/Severity 是生成游标时的过滤条件（冗余记录，用于可调试）。
	Namespace string `json:"ns,omitempty"`
	Phase     string `json:"phase,omitempty"`
	Severity  string `json:"severity,omitempty"`
	// ScopeIndex 是未指定 namespace 时当前扫描到的授权命名空间索引。
	ScopeIndex int `json:"scopeIndex,omitempty"`
	// ScopeHash 绑定当前授权命名空间集合；部署配置变更后旧游标不可复用。
	ScopeHash string `json:"scopeHash"`
	// Continue 是底层 Kubernetes continue token。
	Continue string `json:"continue,omitempty"`
	// SkipMatched 是当前页内已消费的匹配项数量（M9.4.1 分页修复）：
	// 当某一页过滤后的匹配项超过剩余容量时，cursor 记录该页起点 continue 与
	// 已消费匹配数，下一轮请求重扫该页并跳过前 SkipMatched 个匹配项，
	// 避免整页 continue 跳过页内剩余匹配项（丢数据）。
	SkipMatched int `json:"skipMatched,omitempty"`
	// FilterHash 是过滤条件指纹，用于校验请求条件未变化。
	FilterHash string `json:"fh"`
}

// maxCursorScan 是单次请求的最大扫描对象数。
const maxCursorScan = 2000

// cursorVersion 是当前游标版本。
const cursorVersion = 2

var (
	// errInvalidCursor 是游标解码失败。
	errInvalidCursor = errors.New("continue 参数不是有效的分页游标")
	// errFilterChanged 是过滤条件与游标不一致。
	errFilterChanged = errors.New("过滤条件与分页游标不一致，请重置分页")
)

// filterHashOf 计算过滤条件的稳定指纹。
func filterHashOf(namespace, phase, severity string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s", namespace, phase, severity)))
	return hex.EncodeToString(sum[:6]) // 前 12 个十六进制字符
}

func scopeHashOf(namespace string, namespaces []string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s", namespace, strings.Join(namespaces, ","))))
	return hex.EncodeToString(sum[:6])
}

// encodeCursor 序列化游标。
func encodeCursor(c listCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor 解析游标；任何格式错误都返回 errInvalidCursor。
func decodeCursor(token string) (listCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return listCursor{}, errInvalidCursor
	}
	var c listCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return listCursor{}, errInvalidCursor
	}
	if c.Version != cursorVersion {
		return listCursor{}, errInvalidCursor
	}
	if c.FilterHash == "" || c.ScopeHash == "" || c.ScopeIndex < 0 {
		return listCursor{}, errInvalidCursor
	}
	return c, nil
}

// validateCursor 校验游标与当前过滤条件一致。
func validateCursor(c listCursor, opts ListOptions, namespaces []string) error {
	if c.FilterHash != filterHashOf(opts.Namespace, opts.Phase, opts.Severity) {
		return errFilterChanged
	}
	if c.ScopeHash != scopeHashOf(opts.Namespace, namespaces) || c.ScopeIndex >= len(namespaces) {
		return errFilterChanged
	}
	return nil
}
