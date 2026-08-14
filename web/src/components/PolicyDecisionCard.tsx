import type { AIOpsIncident } from "../api/types";
import EmptyState from "./EmptyState";

interface PolicyDecisionCardProps {
  incident: AIOpsIncident;
}

function PolicyDecisionCard({ incident }: PolicyDecisionCardProps) {
  const pd = incident.status.policyDecision;
  const proposal = incident.status.proposal;

  if (!pd && !proposal) {
    return <EmptyState message="尚未评估策略 (Unavailable)" />;
  }

  const decision = pd?.decision ?? "未提供 (Unavailable)";
  const risk = proposal?.risk ? proposal.risk.toUpperCase() : "未提供 (Unavailable)";

  return (
    <div className="policy-decision-card">
      <p>
        <strong>策略决策: </strong>
        <span className={`decision-badge decision-${pd?.decision ?? "unknown"}`}>
          {decision}
        </span>
      </p>
      <p>
        <strong>风险等级: </strong>
        <span className={`risk-badge risk-${proposal?.risk ?? "unknown"}`}>
          {risk}
        </span>
      </p>
      {pd?.policyRef && (
        <p>
          <strong>生效策略: </strong>
          <code>{pd.policyRef}</code>
        </p>
      )}
      {pd?.approvalTTL && (
        <p>
          <strong>审批有效期: </strong>
          <span>{pd.approvalTTL}</span>
        </p>
      )}
      {pd?.reasonCodes && pd.reasonCodes.length > 0 && (
        <p>
          <strong>判定原因: </strong>
          <span className="reason-codes">{pd.reasonCodes.join(", ")}</span>
        </p>
      )}
    </div>
  );
}

export default PolicyDecisionCard;


