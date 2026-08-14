import { useParams, Link } from "react-router-dom";
import { useIncident } from "../hooks/useIncident";
import {
  useIncidentEvidence,
  useIncidentTimeline,
} from "../hooks/useIncidentDetails";
import PhaseStepper from "../components/PhaseStepper";
import LoadingState from "../components/LoadingState";
import EmptyState from "../components/EmptyState";
import ApprovalActions from "../components/ApprovalActions";
import EvidencePanel from "../components/EvidencePanel";
import DiagnosisCard from "../components/DiagnosisCard";
import AuditTimeline from "../components/AuditTimeline";
import PolicyDecisionCard from "../components/PolicyDecisionCard";
import ExecutionCard from "../components/ExecutionCard";
import AlertBanner from "../components/AlertBanner";

function IncidentDetailPage() {
  const { namespace = "", name = "" } = useParams();
  const { data, isLoading, isError } = useIncident(namespace, name);
  const timelineQuery = useIncidentTimeline(namespace, name);
  const evidenceQuery = useIncidentEvidence(namespace, name);

  const duration = (startedAt: string, resolvedAt?: string) => {
    const start = new Date(startedAt).getTime();
    const end = resolvedAt ? new Date(resolvedAt).getTime() : Date.now();
    if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) {
      return "—";
    }
    const seconds = Math.round((end - start) / 1000);
    if (seconds < 60) return `${seconds}s`;
    const minutes = Math.floor(seconds / 60);
    return `${minutes}m ${seconds % 60}s`;
  };

  return (
    <main className="incident-detail">
      <nav className="breadcrumb">
        <Link to="/">← 返回列表</Link>
      </nav>

      {isLoading && <LoadingState />}
      {isError && (
        <div role="alert" className="error-state">
          加载失败，请确认事故存在。
        </div>
      )}
      {data && (
        <>
          <header className="page-header incident-header">
            <div>
              <p className="product-eyebrow">INCIDENT COMMAND · {data.spec.cluster}</p>
              <h1>{namespace}/{name}</h1>
              <p className="page-lede">
                {data.spec.targetRef.kind}/{data.spec.targetRef.name} · {data.spec.alertName}
              </p>
            </div>
            <div className="detail-meta">
              <span className={`severity-badge severity-${data.spec.severity}`}>
                {data.spec.severity}
              </span>
              <span className={`phase-badge phase-${data.status.phase}`}>
                {data.status.phase ?? "—"}
              </span>
              <span className="cloud-badge">ALIYUN / K3S / CONTROLLED DEMO</span>
            </div>
          </header>

          <section className="incident-snapshot" aria-label="事故处置摘要">
            <article>
              <span className="snapshot-label">SIGNAL</span>
              <strong>{data.spec.alertName}</strong>
              <small>{data.spec.severity.toUpperCase()} · {data.spec.sourceStatus}</small>
            </article>
            <article>
              <span className="snapshot-label">DIAGNOSIS</span>
              <strong>{data.status.diagnosis?.category ?? "PENDING"}</strong>
              <small>
                {data.status.diagnosis?.confidence !== undefined
                  ? `${Math.round(data.status.diagnosis.confidence * 100)}% confidence`
                  : "等待证据收敛"}
              </small>
            </article>
            <article>
              <span className="snapshot-label">GUARDRAIL</span>
              <strong>{data.status.proposal?.risk?.toUpperCase() ?? "PENDING"} RISK</strong>
              <small>{data.status.policyDecision?.decision ?? "策略检查中"}</small>
            </article>
            <article className={data.status.phase === "Resolved" ? "snapshot-success" : "snapshot-active"}>
              <span className="snapshot-label">OUTCOME</span>
              <strong>{data.status.phase}</strong>
              <small>{data.status.phase === "Resolved" ? "验证通过" : "受控流程进行中"} · {duration(data.spec.startedAt, data.spec.resolvedAt)}</small>
            </article>
          </section>

          <PhaseStepper phase={data.status.phase} />

          <ApprovalActions incident={data} />

          {(timelineQuery.data?.detailsUnavailable ||
            evidenceQuery.data?.detailsUnavailable) && (
            <AlertBanner message="诊断服务部分接口不可用，已降级显示 Incident 内数据。" />
          )}

          <section className="detail-grid">
            <div className="card card-identity">
              <h2>基本信息</h2>
              <dl>
                <dt>指纹</dt>
                <dd className="mono">{data.spec.fingerprint.slice(0, 24)}…</dd>
                <dt>集群</dt>
                <dd>{data.spec.cluster}</dd>
                <dt>开始时间</dt>
                <dd>{new Date(data.spec.startedAt).toLocaleString()}</dd>
                <dt>最后接收</dt>
                <dd>
                  {new Date(
                    data.spec.lastReceivedAt ?? data.spec.startedAt,
                  ).toLocaleString()}
                </dd>
              </dl>
            </div>

            <div className="card card-diagnosis">
              <h2>诊断</h2>
              <DiagnosisCard incident={data} />
            </div>

            <div className="card card-policy">
              <h2>方案与策略</h2>
              {!data.status.proposal && <EmptyState message="尚无方案" />}
              {data.status.proposal && (
                <p>
                  <strong>方案: </strong>
                  {data.status.proposal.action}
                  {data.status.proposal.parameters &&
                    Object.keys(data.status.proposal.parameters).length > 0 && (
                      <span className="proposal-params">
                        {" "}
                        {JSON.stringify(data.status.proposal.parameters)}
                      </span>
                    )}
                  {data.status.proposal.planDigest && (
                    <span className="mono">
                      {" "}
                      ({data.status.proposal.planDigest.slice(0, 24)}…)
                    </span>
                  )}
                </p>
              )}
              <PolicyDecisionCard incident={data} />
            </div>

            <div className="card card-evidence">
              <h2>证据</h2>
              <EvidencePanel evidence={evidenceQuery.data} />
            </div>

            <div className="card card-execution">
              <h2>执行</h2>
              <ExecutionCard incident={data} />
            </div>

            <div className="card card-timeline">
              <h2>时间线</h2>
              <AuditTimeline
                items={timelineQuery.data?.items ?? data.status.timeline}
                source={timelineQuery.data?.source}
                detailsUnavailable={timelineQuery.data?.detailsUnavailable}
              />
            </div>
          </section>
        </>
      )}
    </main>
  );
}

export default IncidentDetailPage;
