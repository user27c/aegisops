import type { EvidenceResponse } from "../api/types";
import EmptyState from "./EmptyState";

interface EvidencePanelProps {
  evidence?: EvidenceResponse;
}

function EvidencePanel({ evidence }: EvidencePanelProps) {
  if (!evidence) {
    return <EmptyState message="尚无证据" />;
  }
  const unavailable = evidence.detailsUnavailable === true;
  return (
    <div className="evidence-panel">
      {unavailable && (
        <p className="notice" role="status">
          证据详情暂不可用（诊断服务不可达），以下为 Incident 内的概要信息。
        </p>
      )}
      <div className="evidence-meta-bar">
        <div>
          <span className="meta-key">证据快照哈希: </span>
          <code className="mono">{evidence.hash ? `${evidence.hash.slice(0, 24)}…` : "—"}</code>
          {evidence.partial && <span className="badge badge-warning">部分缺失</span>}
        </div>
        <div>
          <span className="meta-key">采集时间窗口: </span>
          <span className="meta-val">
            {evidence.windowStart ? new Date(evidence.windowStart).toLocaleTimeString() : "?"} ~{" "}
            {evidence.windowEnd ? new Date(evidence.windowEnd).toLocaleTimeString() : "?"}
          </span>
        </div>
      </div>
      {evidence.missingSources && evidence.missingSources.length > 0 && (
        <p className="notice">缺失来源: {evidence.missingSources.join(", ")}</p>
      )}
      {typeof evidence.redactions === "number" && evidence.redactions > 0 && (
        <p className="notice">已脱敏 {evidence.redactions} 处敏感信息</p>
      )}
      {(!evidence.items || evidence.items.length === 0) && (
        <EmptyState message="证据条目为空" />
      )}
      {evidence.items && evidence.items.length > 0 && (
        <table className="evidence-table">
          <thead>
            <tr>
              <th>证据类型</th>
              <th>数据源</th>
              <th>采集时间</th>
              <th>证据内容摘要</th>
            </tr>
          </thead>
          <tbody>
            {evidence.items.map((item) => (
              <tr key={item.id}>
                <td>
                  <span className="evidence-kind-badge">{item.kind}</span>
                </td>
                <td>{item.source ?? "—"}</td>
                <td className="mono">
                  {item.timestamp ? new Date(item.timestamp).toLocaleTimeString() : "—"}
                </td>
                <td className="evidence-summary-cell">{item.summary ?? "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

export default EvidencePanel;


