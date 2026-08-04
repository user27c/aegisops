import type { AIOpsIncident } from "../api/types";
import EmptyState from "./EmptyState";

interface DiagnosisCardProps {
  incident: AIOpsIncident;
}

function DiagnosisCard({ incident }: DiagnosisCardProps) {
  const d = incident.status.diagnosis;
  if (!d) {
    return <EmptyState message="尚未诊断" />;
  }
  return (
    <div className="diagnosis-card">
      <p>
        <strong>根因: </strong>
        {d.rootCause ?? d.category ?? "未知"}
      </p>
      {d.confidence !== undefined && (
        <p>
          <strong>置信度: </strong>
          {Math.round(d.confidence * 100)}%
        </p>
      )}
      {d.evidenceIDs && d.evidenceIDs.length > 0 && (
        <p>
          <strong>证据引用: </strong>
          {d.evidenceIDs.join(", ")}
        </p>
      )}
      {d.runbookRefs && d.runbookRefs.length > 0 && (
        <p>
          <strong>Runbook: </strong>
          {d.runbookRefs.join(", ")}
        </p>
      )}
    </div>
  );
}

export default DiagnosisCard;
