package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	opsv1alpha1 "github.com/user27c/aegisops/api/v1alpha1"
	"github.com/user27c/aegisops/internal/observability"
)

// SchemaVersion 是证据包格式版本。
const SchemaVersion = "v1"

// CollectorVersion 参与证据哈希（采集逻辑变化时递增，使旧证据失效）。
const CollectorVersion = "collector-v1"

// K8sCollector 采集 Kubernetes 证据（必需源）。
type K8sCollector interface {
	Collect(ctx context.Context, incident *opsv1alpha1.AIOpsIncident) ([]EvidenceItem, TargetSnapshot, error)
}

// PromClient 查询 Prometheus。
type PromClient interface {
	Query(ctx context.Context, promQL string, ts time.Time) (json.RawMessage, error)
	QueryRange(ctx context.Context, promQL string, start, end time.Time, stepSeconds int) (json.RawMessage, error)
}

// LokiClient 查询 Loki。
type LokiClient interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]LogLine, error)
}

// TempoClient 查询 Trace（MVP 可选）。
type TempoClient interface {
	SummarizeTrace(ctx context.Context, traceID string) ([]EvidenceItem, error)
}

// Redactor 脱敏文本。
type Redactor interface {
	// RedactString 返回脱敏后的文本与脱敏事件。
	RedactString(s string) (string, []Redaction)
}

// Limits 是采集限制。
type Limits struct {
	// MaxConcurrent 是并发采集数上限。
	MaxConcurrent int
	// QueryTimeout 是单次外部查询超时。
	QueryTimeout time.Duration
	// MaxPackBytes 是证据包字节上限。
	MaxPackBytes int
}

// DefaultLimits 返回默认限制。
func DefaultLimits() Limits {
	return Limits{
		MaxConcurrent: 4,
		QueryTimeout:  5 * time.Second,
		MaxPackBytes:  MaxPackBytes,
	}
}

// MultiCollector 聚合多源采集器，并做脱敏、截断与哈希。
type MultiCollector struct {
	K8s    K8sCollector
	Prom   PromClient
	Loki   LokiClient
	Tempo  TempoClient
	Redact Redactor
	Limits Limits
	Now    func() time.Time
}

// Collect 采集并打包证据。
//
// 规则：
//   - K8s 是必需源，失败则整体失败（不调用 LLM）。
//   - Prom/Loki 失败只标记 partial，不使整次采集失败。
//   - 并发上限 Limits.MaxConcurrent。
func (c *MultiCollector) Collect(ctx context.Context, incident *opsv1alpha1.AIOpsIncident) (EvidencePack, error) {
	if c.Now == nil {
		c.Now = time.Now
	}
	limits := c.Limits
	if limits.MaxConcurrent <= 0 {
		limits = DefaultLimits()
	}

	// The collection window opens before any source is queried, but it must not
	// close until every snapshot has been taken.  Capturing ``end`` here used to
	// create self-inconsistent packs: Target.ObservedAt and rollout evidence
	// could be a few milliseconds after Window.End.
	collectionStartedAt := c.Now()
	start := collectionStartedAt.Add(-DefaultEvidenceWindow)
	pack := EvidencePack{
		SchemaVersion:    SchemaVersion,
		CollectorVersion: CollectorVersion,
		IncidentUID:      incident.UID,
		Window:           TimeWindow{Start: start},
	}

	// 必需源：K8s。
	k8sCtx, k8sSpan := observability.Tracer("aegisops-operator").Start(ctx, "evidence.k8s_collect")
	k8sItems, snapshot, err := c.K8s.Collect(k8sCtx, incident)
	k8sSpan.End()
	if err != nil {
		return EvidencePack{}, fmt.Errorf("K8s 证据采集失败: %w", err)
	}
	pack.Target = snapshot
	pack.Items = append(pack.Items, k8sItems...)

	// 可选源：并发执行，单个失败只标记 partial。
	var promItems, lokiItems, tempoItems []EvidenceItem
	ctxTimeout, cancel := context.WithTimeout(ctx, limits.QueryTimeout)
	defer cancel()

	g, gctx := errgroup.WithContext(ctxTimeout)
	g.SetLimit(limits.MaxConcurrent)

	g.Go(func() error {
		pCtx, pSpan := observability.Tracer("aegisops-operator").Start(gctx, "evidence.prom_collect")
		defer pSpan.End()
		items, err := c.collectProm(pCtx, incident, start, collectionStartedAt)
		if err != nil {
			// 可选源失败只标记 partial，不使整体采集失败（nilerr 有意为之）。
			pack.Partial = true
			pack.MissingSources = append(pack.MissingSources, "prometheus")
			return nil //nolint:nilerr
		}
		promItems = items
		return nil
	})
	g.Go(func() error {
		lCtx, lSpan := observability.Tracer("aegisops-operator").Start(gctx, "evidence.loki_collect")
		defer lSpan.End()
		items, err := c.collectLoki(lCtx, incident, start, collectionStartedAt)
		if err != nil {
			// 可选源失败只标记 partial（nilerr 有意为之）。
			pack.Partial = true
			pack.MissingSources = append(pack.MissingSources, "loki")
			return nil //nolint:nilerr
		}
		lokiItems = items
		return nil
	})
	if c.Tempo != nil {
		g.Go(func() error {
			tCtx, tSpan := observability.Tracer("aegisops-operator").Start(gctx, "evidence.tempo_collect")
			defer tSpan.End()
			items, err := c.collectTempo(tCtx, incident, start, collectionStartedAt)
			if err != nil {
				// 可选源失败只标记 partial（nilerr 有意为之）。
				pack.Partial = true
				pack.MissingSources = append(pack.MissingSources, "tempo")
				return nil //nolint:nilerr
			}
			tempoItems = items
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return EvidencePack{}, fmt.Errorf("可选源采集异常: %w", err)
	}
	// Keep the window truthful for every item gathered above.  Optional queries
	// may have used the initial timestamp as their end bound, which is safe; the
	// published evidence window is allowed to be wider, never narrower.
	pack.Window.End = c.Now()

	pack.Items = append(pack.Items, promItems...)
	pack.Items = append(pack.Items, lokiItems...)
	pack.Items = append(pack.Items, tempoItems...)

	_, finSpan := observability.Tracer("aegisops-operator").Start(ctx, "evidence.finalize")
	err = finalizePack(&pack, c.Redact, limits.MaxPackBytes)
	finSpan.End()
	if err != nil {
		return EvidencePack{}, err
	}
	return pack, nil
}

func (c *MultiCollector) collectProm(ctx context.Context, incident *opsv1alpha1.AIOpsIncident, start, end time.Time) ([]EvidenceItem, error) {
	if c.Prom == nil {
		return nil, nil
	}
	specs, err := QueriesForIncident(incident)
	if err != nil {
		return nil, err
	}
	items := make([]EvidenceItem, 0, len(specs))
	for _, spec := range specs {
		query, err := RenderQuery(spec, safeLabelsFor(incident))
		if err != nil {
			return nil, err
		}
		raw, err := c.Prom.QueryRange(ctx, query, start, end, 60)
		if err != nil {
			return nil, err
		}
		items = append(items, EvidenceItem{
			ID:        spec.ID,
			Kind:      KindMetricSeries,
			Source:    "prometheus/" + spec.ID,
			Timestamp: end,
			Summary:   spec.Description,
			Payload:   raw,
		})
	}
	return items, nil
}

func (c *MultiCollector) collectLoki(ctx context.Context, incident *opsv1alpha1.AIOpsIncident, start, end time.Time) ([]EvidenceItem, error) {
	if c.Loki == nil {
		return nil, nil
	}
	podSelector, err := podSelectorFor(incident)
	if err != nil {
		return nil, err
	}
	query := BuildSafeLogQL(incident.Spec.TargetRef.Namespace, podSelector)
	lines, err := c.Loki.QueryRange(ctx, query, start, end, MaxLogLinesPerSource)
	if err != nil {
		return nil, err
	}
	return LogsToEvidence(lines, c.Redact, MaxLogLinesPerSource, MaxLogLineBytes)
}

func (c *MultiCollector) collectTempo(_ context.Context, _ *opsv1alpha1.AIOpsIncident, _ time.Time, _ time.Time) ([]EvidenceItem, error) {
	if c.Tempo == nil {
		return nil, nil
	}
	// MVP：只根据日志中提取的 TraceID 查询；无 Trace 时返回 NotAvailable。
	return []EvidenceItem{{
		ID:      "trace-0",
		Kind:    KindTraceSummary,
		Source:  "tempo",
		Summary: "NotAvailable: MVP 阶段仅按需查询 Trace",
	}}, nil
}

// finalizePack 脱敏、限流并计算哈希。
func finalizePack(pack *EvidencePack, redactor Redactor, maxBytes int) error {
	// 逐条脱敏。
	for idx := range pack.Items {
		item := &pack.Items[idx]
		if len(item.Summary) > 0 && redactor != nil {
			redacted, redactions := redactor.RedactString(item.Summary)
			item.Summary = redacted
			pack.Redactions = append(pack.Redactions, redactions...)
		}
		if len(item.Payload) > 0 && redactor != nil {
			redacted, redactions, err := redactPayloadJSON(item.Payload, redactor)
			if err != nil {
				return fmt.Errorf("脱敏 evidence payload: %w", err)
			}
			item.Payload = redacted
			pack.Redactions = append(pack.Redactions, redactions...)
		}
	}

	// 总量限流。
	limited, report := LimitItems(pack.Items, maxBytes)
	pack.Items = limited
	if report.Truncated {
		pack.Items = append(pack.Items, EvidenceItem{
			ID:      "limit-report",
			Kind:    KindTargetSnapshot,
			Source:  "collector",
			Summary: fmt.Sprintf("证据已截断: 原始 %d 字节 → %d 字节，丢弃 %d 条", report.OriginalBytes, report.FinalBytes, report.Dropped),
		})
	}

	// 稳定哈希。
	pack.Hash = HashPack(*pack)
	return nil
}

// redactPayloadJSON 对结构化来源（尤其 Prometheus label values）递归脱敏。
// Payload 进入诊断服务和评估导出前必须与 Summary 一样通过 Redactor；无法解析的
// 原始 JSON 视为不可信输入，fail closed 而不是原样透传。
func redactPayloadJSON(raw json.RawMessage, redactor Redactor) (json.RawMessage, []Redaction, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, nil, fmt.Errorf("payload 不是合法 JSON: %w", err)
	}
	redacted, events := redactJSONValue(value, redactor)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return nil, nil, fmt.Errorf("序列化脱敏 payload: %w", err)
	}
	return encoded, events, nil
}

func redactJSONValue(value any, redactor Redactor) (any, []Redaction) {
	switch typed := value.(type) {
	case string:
		redacted, events := redactor.RedactString(typed)
		return redacted, events
	case []any:
		events := []Redaction{}
		for index := range typed {
			redacted, itemEvents := redactJSONValue(typed[index], redactor)
			typed[index] = redacted
			events = append(events, itemEvents...)
		}
		return typed, events
	case map[string]any:
		events := []Redaction{}
		for key, item := range typed {
			if sensitivePayloadField(key) {
				if _, isString := item.(string); isString {
					typed[key] = "field-REDACTED"
					events = append(events, Redaction{Pattern: "sensitive-payload-field", Count: 1})
					continue
				}
			}
			redacted, itemEvents := redactJSONValue(item, redactor)
			typed[key] = redacted
			events = append(events, itemEvents...)
		}
		return typed, events
	default:
		return value, nil
	}
}

func sensitivePayloadField(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	return strings.Contains(normalized, "token") || strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "password") || strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "accesskey")
}

// HashPack 计算证据包内容哈希（幂等键与去重用）。
func HashPack(pack EvidencePack) string {
	// 只对稳定语义字段哈希，排除 Hash 本身。
	pack.Hash = ""
	raw, err := json.Marshal(pack)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// safeLabelsFor 构造查询用的安全标签（regex escape 由 RenderQuery 处理）。
func safeLabelsFor(incident *opsv1alpha1.AIOpsIncident) SafeLabels {
	return SafeLabels{
		Namespace: incident.Spec.TargetRef.Namespace,
		Workload:  incident.Spec.TargetRef.Name,
	}
}
