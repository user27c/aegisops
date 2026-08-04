import type { AIOpsIncident } from "../api/types";

interface ExecutionCardProps {
  incident: AIOpsIncident;
}

function ExecutionCard({ incident }: ExecutionCardProps) {
  const ex = incident.status.execution;
  if (!ex) {
    return null;
  }
  const lock = ex.targetLock;
  return (
    <div className="execution-card">
      {ex.reference?.operationID && (
        <p>
          <strong>执行 ID: </strong>
          <span className="mono">{ex.reference.operationID.slice(0, 24)}…</span>
        </p>
      )}
      {lock && (
        <p>
          <strong>目标锁: </strong>
          <span className="mono">{lock.leaseName}</span>
          {lock.holderIdentity && (
            <> holder={lock.holderIdentity.slice(0, 8)}…</>
          )}
        </p>
      )}
      {ex.attempts !== undefined && (
        <p>
          <strong>尝试次数: </strong>
          {ex.attempts}
        </p>
      )}
    </div>
  );
}

export default ExecutionCard;
