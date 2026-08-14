import type { AIOpsIncident } from "../api/types";
import EmptyState from "./EmptyState";

interface DiagnosisCardProps {
  incident: AIOpsIncident;
}

function DiagnosisCard({ incident }: DiagnosisCardProps) {
  const d = incident.status.diagnosis;
  if (!d) {
    return <EmptyState message="尚未生成诊断" />;
  }
  return (
    <div className="diagnosis-card">
      <div className="diagnosis-row">
        <span className="diag-label">根因结论 (Root Cause):</span>
        <strong className="diag-highlight">{d.rootCause ?? d.category ?? "未知"}</strong>
      </div>
      <div className="diagnosis-meta-grid">
        <div>
          <span className="diag-label">故障分类:</span>
          <span className="diag-text">{d.category ?? "—"}</span>
        </div>
        <div>
          <span className="diag-label">置信度:</span>
          <span className="confidence-badge">
            {d.confidence !== undefined ? `${Math.round(d.confidence * 100)}%` : "—"}
          </span>
        </div>
      </div>
      {d.evidenceIDs && d.evidenceIDs.length > 0 && (
        <div className="diagnosis-row">
          <span className="diag-label">关联证据:</span>
          <div className="evidence-tags">
            {d.evidenceIDs.map((id) => (
              <code key={id} className="mono evidence-tag">{id}</code>
            ))}
          </div>
        </div>
      )}
      {d.runbookRefs && d.runbookRefs.length > 0 && (
        <div className="diagnosis-row">
          <span className="diag-label">参考 Runbook:</span>
          <span className="runbook-ref">{d.runbookRefs.join(", ")}</span>
        </div>
      )}
    </div>
  );
}

export default DiagnosisCard;

