package evidence

import "encoding/json"

// LimitReport 是限流报告。
type LimitReport struct {
	// OriginalBytes 是原始总字节数。
	OriginalBytes int
	// FinalBytes 是限流后总字节数。
	FinalBytes int
	// Dropped 是丢弃的条目数。
	Dropped int
	// Truncated 标记是否发生了截断。
	Truncated bool
}

// LimitItems 按字节上限裁剪证据条目：保留更早的条目，丢弃超限后的。
// 确定性顺序：保持输入顺序。
func LimitItems(items []EvidenceItem, maxBytes int) ([]EvidenceItem, LimitReport) {
	if maxBytes <= 0 {
		maxBytes = MaxPackBytes
	}
	report := LimitReport{}
	out := make([]EvidenceItem, 0, len(items))
	for _, item := range items {
		size := itemSize(item)
		report.OriginalBytes += size
		if report.FinalBytes+size > maxBytes {
			report.Dropped++
			report.Truncated = true
			continue
		}
		out = append(out, item)
		report.FinalBytes += size
	}
	return out, report
}

// itemSize 估算单条证据的序列化大小。
func itemSize(item EvidenceItem) int {
	raw, err := json.Marshal(item)
	if err != nil {
		return len(item.Summary) + 64
	}
	return len(raw)
}
