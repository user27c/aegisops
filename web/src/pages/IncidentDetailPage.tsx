import { useParams, Link } from 'react-router-dom'
import { useIncident } from '../hooks/useIncident'
import PhaseStepper from '../components/PhaseStepper'
import LoadingState from '../components/LoadingState'
import EmptyState from '../components/EmptyState'
import ApprovalActions from '../components/ApprovalActions'

/** 单事故详情页：阶段、证据、诊断、方案与审批（M1 展示基础信息）。 */
function IncidentDetailPage() {
  const { namespace = '', name = '' } = useParams()
  const { data, isLoading, isError } = useIncident(namespace, name)

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
          <header className="page-header">
            <h1>
              {namespace}/{name}
            </h1>
            <div className="detail-meta">
              <span className={`severity-badge severity-${data.spec.severity}`}>{data.spec.severity}</span>
              <span className={`phase-badge phase-${data.status.phase}`}>{data.status.phase ?? '—'}</span>
              <span>
                目标: {data.spec.targetRef.kind}/{data.spec.targetRef.name}
              </span>
              <span>告警: {data.spec.alertName}</span>
              <span>来源: {data.spec.sourceStatus}</span>
            </div>
          </header>

          <PhaseStepper phase={data.status.phase} />

          <ApprovalActions incident={data} />

          <section className="detail-grid">
            <div className="card">
              <h2>基本信息</h2>
              <dl>
                <dt>指纹</dt>
                <dd className="mono">{data.spec.fingerprint.slice(0, 24)}…</dd>
                <dt>集群</dt>
                <dd>{data.spec.cluster}</dd>
                <dt>开始时间</dt>
                <dd>{new Date(data.spec.startedAt).toLocaleString()}</dd>
                <dt>最后接收</dt>
                <dd>{new Date(data.spec.lastReceivedAt ?? data.spec.startedAt).toLocaleString()}</dd>
              </dl>
            </div>

            <div className="card">
              <h2>诊断与方案</h2>
              {!data.status.diagnosis && !data.status.proposal && <EmptyState message="尚未诊断" />}
              {data.status.diagnosis && (
                <>
                  <p>
                    <strong>根因: </strong>
                    {data.status.diagnosis.rootCause ?? data.status.diagnosis.category ?? '未知'}
                  </p>
                  {data.status.diagnosis.confidence !== undefined && (
                    <p>
                      <strong>置信度: </strong>
                      {Math.round(data.status.diagnosis.confidence * 100)}%
                    </p>
                  )}
                  {data.status.diagnosis.evidenceIDs && data.status.diagnosis.evidenceIDs.length > 0 && (
                    <p>
                      <strong>证据引用: </strong>
                      {data.status.diagnosis.evidenceIDs.join(', ')}
                    </p>
                  )}
                </>
              )}
              {data.status.proposal && (
                <p>
                  <strong>方案: </strong>
                  {data.status.proposal.action}
                  {data.status.proposal.planDigest && (
                    <span className="mono"> ({data.status.proposal.planDigest.slice(0, 24)}…)</span>
                  )}
                </p>
              )}
            </div>

            <div className="card">
              <h2>时间线</h2>
              {!data.status.timeline || data.status.timeline.length === 0 ? (
                <EmptyState message="暂无时间线" />
              ) : (
                <ul className="timeline">
                  {data.status.timeline.map((e, idx) => (
                    <li key={idx}>
                      <span className="mono">{new Date(e.time).toLocaleTimeString()}</span> {e.type}
                      {e.message && <span className="timeline-message"> — {e.message}</span>}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </section>
        </>
      )}
    </main>
  )
}

export default IncidentDetailPage
