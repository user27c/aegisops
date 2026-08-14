import { useParams, Link } from "react-router-dom";
import { useIncident } from "../hooks/useIncident";
import {
  useIncidentEvidence,
  useIncidentTimeline,
} from "../hooks/useIncidentDetails";
import PhaseStepper from "../components/PhaseStepper";
import LoadingState from "../components/LoadingState";
import ApprovalActions from "../components/ApprovalActions";
import EvidencePanel from "../components/EvidencePanel";
import DiagnosisCard from "../components/DiagnosisCard";
import AuditTimeline from "../components/AuditTimeline";
import PolicyDecisionCard from "../components/PolicyDecisionCard";
import ExecutionCard from "../components/ExecutionCard";
import AlertBanner from "../components/AlertBanner";

function formatDuration(startedAt: string, resolvedAt?: string) {
  const start = new Date(startedAt).getTime();
  const end = resolvedAt ? new Date(resolvedAt).getTime() : Date.now();
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) {
    return "—";
  }
  const seconds = Math.round((end - start) / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  return `${minutes}m ${seconds % 60}s`;
}

function IncidentDetailPage() {
  const { namespace = "", name = "" } = useParams();
  const { data, isLoading, isError } = useIncident(namespace, name);
  const timelineQuery = useIncidentTimeline(namespace, name);
  const evidenceQuery = useIncidentEvidence(namespace, name);

  const isResolved = data?.status.phase === "Resolved";
  const durationText = data
    ? formatDuration(data.spec.startedAt, data.spec.resolvedAt)
    : "—";

  // 数据一致性检查 (Consistency Warning)
  const hasSignalPhaseConflict =
    isResolved && data?.spec.sourceStatus === "firing";

  return (
    <div className="incident-detail-page">
      {/* 顶部应用返回导航 */}
      <nav className="detail-nav-bar" aria-label="页面导航">
        <Link to="/" className="back-link">
          ← 返回事故列表
        </Link>
      </nav>

      {isLoading && <LoadingState />}
      {isError && (
        <div role="alert" className="error-state">
          加载失败，请确认事故存在或网络连接正常。
        </div>
      )}

      {data && (
        <main className="detail-content-container">
          {/* 事故标题与元数据头部 */}
          <header className="incident-page-header">
            <div className="header-primary">
              <h1 className="incident-title">
                {namespace}/{name}
              </h1>
              <p className="incident-target-subtitle">
                {data.spec.targetRef.kind}/{data.spec.targetRef.name} · 告警: {data.spec.alertName}
              </p>
            </div>
            <div className="header-badges">
              <span className={`badge badge-severity severity-${data.spec.severity}`}>
                {data.spec.severity}
              </span>
              <span className={`badge badge-phase phase-${data.status.phase}`}>
                {data.status.phase ?? "—"}
              </span>
              <span className="badge badge-duration">
                {data.spec.resolvedAt ? `MTTR: ${durationText}` : `耗时 (进行中): ${durationText}`}
              </span>
              <span className="badge badge-cluster">
                集群: {data.spec.cluster ?? "未指定 (Unavailable)"}
              </span>
            </div>
          </header>

          {/* 阶段状态机流水线 */}
          <PhaseStepper phase={data.status.phase} />

          {/* 数据一致性告警 */}
          {hasSignalPhaseConflict && (
            <div className="consistency-warning-banner" role="alert">
              ⚠️ <strong>数据一致性提示 (Consistency Warning)</strong>: 事故状态机已进入 <code>Resolved</code>，但 <code>spec.sourceStatus</code> 仍为 <code>firing</code>（等待 Alertmanager 发送 resolved 消除通知）。
            </div>
          )}

          {(timelineQuery.data?.detailsUnavailable ||
            evidenceQuery.data?.detailsUnavailable) && (
            <AlertBanner message="可观测性诊断服务部分接口不可用，已平滑降级展示 Incident 核心数据。" />
          )}

          {/* 两栏经典运维控制台布局 */}
          <div className="incident-two-column-layout">
            {/* 左侧主区域 */}
            <div className="column-main">
              {/* 1. Overview 概览 */}
              <section className="card section-overview" aria-labelledby="heading-overview">
                <div className="card-header">
                  <h2 id="heading-overview">1. 事故概览 (Overview)</h2>
                </div>
                <div className="overview-details-grid">
                  <div className="detail-item">
                    <span className="item-label">指纹 (Fingerprint):</span>
                    <code className="mono">{data.spec.fingerprint}</code>
                  </div>
                  <div className="detail-item">
                    <span className="item-label">目标资源 (Target):</span>
                    <span>{data.spec.targetRef.kind} / {data.spec.targetRef.namespace} / {data.spec.targetRef.name}</span>
                  </div>
                  <div className="detail-item">
                    <span className="item-label">信号状态 (spec.sourceStatus):</span>
                    <span className={data.spec.sourceStatus === "resolved" ? "signal-tag signal-resolved" : "signal-tag signal-firing"}>
                      {data.spec.sourceStatus}
                    </span>
                  </div>
                  <div className="detail-item">
                    <span className="item-label">开始时间 (Started At):</span>
                    <span className="mono">{new Date(data.spec.startedAt).toLocaleString()}</span>
                  </div>
                  <div className="detail-item">
                    <span className="item-label">最后更新 (Updated At):</span>
                    <span className="mono">{new Date(data.spec.lastReceivedAt ?? data.spec.startedAt).toLocaleString()}</span>
                  </div>
                  <div className="detail-item">
                    <span className="item-label">解决时间 (spec.resolvedAt):</span>
                    <span className="mono">
                      {data.spec.resolvedAt ? new Date(data.spec.resolvedAt).toLocaleString() : "未提供 (Unavailable)"}
                    </span>
                  </div>
                </div>
              </section>

              {/* 2. Evidence 证据面板 */}
              <section className="card section-evidence" aria-labelledby="heading-evidence">
                <div className="card-header">
                  <h2 id="heading-evidence">2. 证据链 (Evidence)</h2>
                </div>
                <EvidencePanel evidence={evidenceQuery.data} />
              </section>

              {/* 3. Remediation 自愈执行与生命周期 */}
              <section className="card section-remediation" aria-labelledby="heading-remediation">
                <div className="card-header">
                  <h2 id="heading-remediation">3. 自愈执行闭环 (Remediation & Lifecycle)</h2>
                </div>
                <ExecutionCard incident={data} />
              </section>

              {/* 4. Audit 审计时间线 */}
              <section className="card section-audit" aria-labelledby="heading-audit">
                <div className="card-header">
                  <h2 id="heading-audit">4. 审计日志流 (Audit Timeline)</h2>
                </div>
                <AuditTimeline
                  items={timelineQuery.data?.items ?? data.status.timeline}
                  source={timelineQuery.data?.source}
                  detailsUnavailable={timelineQuery.data?.detailsUnavailable}
                />
              </section>
            </div>

            {/* 右侧固定摘要栏 */}
            <aside className="column-sidebar" aria-label="事故摘要与决策">
              {/* 1. Diagnosis 诊断 */}
              <div className="card sidebar-card card-diagnosis">
                <div className="card-header">
                  <h2>诊断结论 (Diagnosis)</h2>
                </div>
                <DiagnosisCard incident={data} />
              </div>

              {/* 2. Risk Decision 策略门禁 */}
              <div className="card sidebar-card card-risk">
                <div className="card-header">
                  <h2>策略门禁 (Risk Decision)</h2>
                </div>
                <PolicyDecisionCard incident={data} />
              </div>

              {/* 3. Proposed Action 方案摘要 */}
              <div className="card sidebar-card card-proposal">
                <div className="card-header">
                  <h2>推荐方案 (Proposed Action)</h2>
                </div>
                {data.status.proposal ? (
                  <div className="proposal-body">
                    <p>
                      <strong>操作类型: </strong>
                      <span className="action-tag">{data.status.proposal.action}</span>
                    </p>
                    {data.status.proposal.parameters && (
                      <p>
                        <strong>变更参数: </strong>
                        <code>{JSON.stringify(data.status.proposal.parameters)}</code>
                      </p>
                    )}
                    {data.status.proposal.planDigest && (
                      <p>
                        <strong>方案摘要 (planDigest): </strong>
                        <code className="mono digest-code">{data.status.proposal.planDigest}</code>
                      </p>
                    )}
                  </div>
                ) : (
                  <p className="text-muted">等待诊断生成方案 (Unavailable)...</p>
                )}
              </div>

              {/* 4. Approval 审批操作与状态 */}
              <div className="card sidebar-card card-approval">
                <div className="card-header">
                  <h2>人工审批 (Human Gate)</h2>
                </div>
                {data.status.phase === "AwaitingApproval" ? (
                  <ApprovalActions incident={data} />
                ) : (
                  <div className="approval-status-box">
                    {data.status.approval?.decision ? (
                      <p className="status-granted">
                        审批决策: <strong>{data.status.approval.decision}</strong>
                        {data.status.approval.actor && (
                          <span> (审批人: <code>@{data.status.approval.actor}</code>)</span>
                        )}
                      </p>
                    ) : (
                      <p className="status-passive text-muted">
                        未记录审批数据 (Unavailable)
                      </p>
                    )}
                  </div>
                )}
              </div>

              {/* 5. Verification 验证结果 */}
              <div className="card sidebar-card card-verification">
                <div className="card-header">
                  <h2>健康核验 (Verification)</h2>
                </div>
                <div className="verification-box">
                  {data.status.verification ? (
                    <div>
                      <p className="verification-status-line">
                        <strong>核验状态: </strong>
                        <span className={`verify-badge verify-${data.status.verification.state?.toLowerCase()}`}>
                          {data.status.verification.state}
                        </span>
                      </p>
                      {data.status.verification.consecutiveSuccesses !== undefined && (
                        <p>
                          <strong>连续成功探测: </strong>
                          <span>{data.status.verification.consecutiveSuccesses} 次</span>
                        </p>
                      )}
                      {data.status.verification.checks && data.status.verification.checks.length > 0 && (
                        <ul className="verify-checks-list">
                          {data.status.verification.checks.map((chk, idx) => (
                            <li key={idx}>
                              <span>{chk.name}: </span>
                              <code>{chk.state}</code>
                              {chk.reason && <span className="text-muted"> ({chk.reason})</span>}
                            </li>
                          ))}
                        </ul>
                      )}
                    </div>
                  ) : (
                    <p className="text-muted">
                      未上报核验状态 (Unavailable)
                    </p>
                  )}
                </div>
              </div>
            </aside>
          </div>
        </main>
      )}
    </div>
  );
}

export default IncidentDetailPage;


