import type { AIOpsIncident } from "../api/types";
import EmptyState from "./EmptyState";

interface PolicyDecisionCardProps {
  incident: AIOpsIncident;
}

function PolicyDecisionCard({ incident }: PolicyDecisionCardProps) {
  const pd = incident.status.policyDecision;
  if (!pd) {
    return <EmptyState message="尚未评估策略" />;
  }
  return (
    <div className="policy-decision-card">
      <p>
        <strong>决策: </strong>
        {pd.decision}
      </p>
      {pd.reasonCodes && pd.reasonCodes.length > 0 && (
        <p>
          <strong>原因: </strong>
          {pd.reasonCodes.join(", ")}
        </p>
      )}
    </div>
  );
}

export default PolicyDecisionCard;
