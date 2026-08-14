import type { AIOpsIncident } from "../api/types";
import EmptyState from "./EmptyState";

interface ExecutionCardProps {
  incident: AIOpsIncident;
}

function ExecutionCard({ incident }: ExecutionCardProps) {
  const ex = incident.status.execution;
  const proposal = incident.status.proposal;
  const verification = incident.status.verification;

  if (!ex && !proposal) {
    return <EmptyState message="暂无方案与执行记录 (Unavailable)" />;
  }

  const action = proposal?.action ?? "未提供 (Unavailable)";
  const parametersStr = proposal?.parameters
    ? JSON.stringify(proposal.parameters, null, 2)
    : "未提供 (Unavailable)";

  const snapshotId = ex?.reference?.snapshotID ?? "未提供 (Unavailable)";
  const operationId = ex?.reference?.operationID ?? "未提供 (Unavailable)";
  const executionId = ex?.reference?.executionID ?? "未提供 (Unavailable)";

  return (
    <div className="execution-remediation-card">
      <div className="remediation-section">
        <h3 className="sub-heading">方案参数 (Proposal)</h3>
        <dl className="property-list">
          <dt>动作类型:</dt>
          <dd><span className="action-tag">{action}</span></dd>
          <dt>动作参数:</dt>
          <dd><code>{parametersStr}</code></dd>
          <dt>方案摘要 (planDigest):</dt>
          <dd><code className="mono">{proposal?.planDigest ?? "未提供 (Unavailable)"}</code></dd>
        </dl>
      </div>

      <div className="remediation-section">
        <h3 className="sub-heading">执行事实与快照 (Execution Facts)</h3>
        <dl className="property-list">
          <dt>执行 ID (Execution ID):</dt>
          <dd className="mono">{executionId}</dd>
          <dt>幂等操作 ID (Operation ID):</dt>
          <dd className="mono">{operationId}</dd>
          <dt>执行前快照 (Snapshot ID):</dt>
          <dd className="mono">{snapshotId}</dd>
          <dt>尝试次数 (Attempts):</dt>
          <dd>{ex?.attempts !== undefined ? ex.attempts : "未记录 (Unavailable)"}</dd>
          <dt>最近错误 (Last Error):</dt>
          <dd>{ex?.lastError ? <span className="text-danger">{ex.lastError}</span> : "无错误上报"}</dd>
        </dl>
      </div>

      <div className="remediation-section">
        <h3 className="sub-heading">目标锁状态 (Target Lock)</h3>
        {ex?.targetLock ? (
          <dl className="property-list">
            <dt>Lease 名称:</dt>
            <dd className="mono">{ex.targetLock.leaseName ?? "—"}</dd>
            <dt>持有者 (Holder):</dt>
            <dd className="mono">{ex.targetLock.holderIdentity ?? "—"}</dd>
            <dt>获取时间:</dt>
            <dd>{ex.targetLock.acquiredAt ? new Date(ex.targetLock.acquiredAt).toLocaleString() : "—"}</dd>
          </dl>
        ) : (
          <p className="text-muted">无目标分布式锁记录 (Unavailable)</p>
        )}
      </div>

      <div className="remediation-section">
        <h3 className="sub-heading">生命周期事实核验 (Lifecycle Checkpoints)</h3>
        <div className="lifecycle-checkpoints">
          <div className={`checkpoint-item ${ex?.targetLock ? "passed" : "pending"}`}>
            <span className="checkpoint-badge">1. Preflight</span>
            <span className="checkpoint-text">
              {ex?.targetLock ? "已获取目标互斥锁" : "未上报锁记录 (Unavailable)"}
            </span>
          </div>
          <div className={`checkpoint-item ${ex?.reference?.snapshotID ? "passed" : "pending"}`}>
            <span className="checkpoint-badge">2. Snapshot</span>
            <span className="checkpoint-text">
              {ex?.reference?.snapshotID ? "已上报原状快照" : "未提供快照 (Unavailable)"}
            </span>
          </div>
          <div className={`checkpoint-item ${ex?.reference?.operationID ? "passed" : "pending"}`}>
            <span className="checkpoint-badge">3. Apply</span>
            <span className="checkpoint-text">
              {ex?.reference?.operationID ? "已上报操作记录" : "未提供写入记录 (Unavailable)"}
            </span>
          </div>
          <div className={`checkpoint-item ${verification?.state === "Healthy" ? "passed" : verification?.state ? "running" : "pending"}`}>
            <span className="checkpoint-badge">4. Verify</span>
            <span className="checkpoint-text">
              {verification?.state ? `核验状态: ${verification.state}` : "未上报核验状态 (Unavailable)"}
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}

export default ExecutionCard;


