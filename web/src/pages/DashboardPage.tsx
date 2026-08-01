import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useIncidents, isTerminal } from '../hooks/useIncidents'
import LoadingState from '../components/LoadingState'
import EmptyState from '../components/EmptyState'

const PHASE_FILTERS = ['', 'Detected', 'AwaitingApproval', 'Executing', 'Verifying', 'Resolved', 'Escalated']
const SEVERITY_FILTERS = ['', 'critical', 'warning', 'info']

/** 事故总览页：状态统计、过滤器、Incident 表格。 */
function DashboardPage() {
  const [namespace, setNamespace] = useState('')
  const [phase, setPhase] = useState('')
  const [severity, setSeverity] = useState('')
  const { data, isLoading, isError, error, dataUpdatedAt } = useIncidents({
    namespace: namespace || undefined,
    phase: phase || undefined,
    severity: severity || undefined,
  })

  const stats = {
    total: data?.items?.length ?? 0,
    active: data?.items?.filter((i) => !isTerminal(i.status.phase)).length ?? 0,
    awaiting: data?.items?.filter((i) => i.status.phase === 'AwaitingApproval').length ?? 0,
    resolved: data?.items?.filter((i) => i.status.phase === 'Resolved').length ?? 0,
    escalated: data?.items?.filter((i) => i.status.phase === 'Escalated').length ?? 0,
  }

  return (
    <main className="dashboard">
      <header className="page-header">
        <h1>AegisOps 事故控制台</h1>
        <span className="updated-at">
          更新于 {dataUpdatedAt ? new Date(dataUpdatedAt).toLocaleTimeString() : '—'}
        </span>
      </header>

      <section className="stats" aria-label="事故统计">
        <div className="stat-card">
          <span className="stat-value">{stats.total}</span>
          <span className="stat-label">全部</span>
        </div>
        <div className="stat-card">
          <span className="stat-value">{stats.active}</span>
          <span className="stat-label">进行中</span>
        </div>
        <div className="stat-card">
          <span className="stat-value">{stats.awaiting}</span>
          <span className="stat-label">待审批</span>
        </div>
        <div className="stat-card">
          <span className="stat-value">{stats.resolved}</span>
          <span className="stat-label">已恢复</span>
        </div>
        <div className="stat-card stat-card-danger">
          <span className="stat-value">{stats.escalated}</span>
          <span className="stat-label">已升级</span>
        </div>
      </section>

      <section className="filters" aria-label="过滤器">
        <input
          type="text"
          placeholder="命名空间"
          value={namespace}
          onChange={(e) => setNamespace(e.target.value)}
          aria-label="按命名空间过滤"
        />
        <select value={phase} onChange={(e) => setPhase(e.target.value)} aria-label="按阶段过滤">
          {PHASE_FILTERS.map((p) => (
            <option key={p} value={p}>
              {p === '' ? '全部阶段' : p}
            </option>
          ))}
        </select>
        <select value={severity} onChange={(e) => setSeverity(e.target.value)} aria-label="按严重级别过滤">
          {SEVERITY_FILTERS.map((s) => (
            <option key={s} value={s}>
              {s === '' ? '全部级别' : s}
            </option>
          ))}
        </select>
      </section>

      {isLoading && <LoadingState />}
      {isError && (
        <div role="alert" className="error-state">
          加载失败: {error instanceof Error ? error.message : String(error)}
        </div>
      )}
      {data && (data.items?.length ?? 0) === 0 && <EmptyState message="暂无事故" />}
      {data && (data.items?.length ?? 0) > 0 && (
        <table className="incident-table">
          <thead>
            <tr>
              <th>名称</th>
              <th>严重级别</th>
              <th>目标</th>
              <th>阶段</th>
              <th>告警</th>
              <th>开始时间</th>
            </tr>
          </thead>
          <tbody>
            {data.items.map((i) => (
              <tr key={i.metadata.uid ?? i.metadata.name}>
                <td>
                  <Link to={`/incidents/${i.metadata.namespace}/${i.metadata.name}`}>{i.metadata.name}</Link>
                </td>
                <td>
                  <span className={`severity-badge severity-${i.spec.severity}`}>{i.spec.severity}</span>
                </td>
                <td>
                  {i.spec.targetRef.kind}/{i.spec.targetRef.name}
                </td>
                <td>
                  <span className={`phase-badge phase-${i.status.phase}`}>{i.status.phase ?? '—'}</span>
                </td>
                <td>{i.spec.alertName}</td>
                <td>{new Date(i.spec.startedAt).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </main>
  )
}

export default DashboardPage
