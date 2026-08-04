import type { EvidenceResponse } from "../api/types";
import EmptyState from "./EmptyState";

interface EvidencePanelProps {
  evidence?: EvidenceResponse;
}

function EvidencePanel({ evidence }: EvidencePanelProps) {
  if (!evidence) {
    return <EmptyState message="尚无证据" />;
  }
  if (evidence.detailsUnavailable) {
    return (
      <p className="notice" role="status">
        证据详情暂不可用（诊断服务不可达），以下为 Incident 内的概要信息。
      </p>
    );
  }
  return (
    <div className="evidence-panel">
      <p>
        哈希: <span className="mono">{evidence.hash?.slice(0, 24)}…</span>
        {evidence.partial && <span className="partial-badge">部分缺失</span>}
      </p>
      <p>
        窗口:{" "}
        {evidence.windowStart
          ? new Date(evidence.windowStart).toLocaleString()
          : "?"}{" "}
        ~{" "}
        {evidence.windowEnd
          ? new Date(evidence.windowEnd).toLocaleString()
          : "?"}
      </p>
      {evidence.missingSources && evidence.missingSources.length > 0 && (
        <p className="notice">缺失来源: {evidence.missingSources.join(", ")}</p>
      )}
      {evidence.redactions && evidence.redactions.length > 0 && (
        <p className="notice">已脱敏 {evidence.redactions.length} 处敏感信息</p>
      )}
      {(!evidence.items || evidence.items.length === 0) && (
        <EmptyState message="证据条目为空" />
      )}
      {evidence.items && evidence.items.length > 0 && (
        <table className="evidence-table">
          <thead>
            <tr>
              <th>类型</th>
              <th>来源</th>
              <th>时间</th>
              <th>摘要</th>
            </tr>
          </thead>
          <tbody>
            {evidence.items.map((item) => (
              <tr key={item.id}>
                <td>{item.kind}</td>
                <td>{item.source ?? "—"}</td>
                <td>
                  {item.timestamp
                    ? new Date(item.timestamp).toLocaleString()
                    : "—"}
                </td>
                <td>{item.summary ?? "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

export default EvidencePanel;
