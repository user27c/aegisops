import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useIncidents, isTerminal } from '../hooks/useIncidents'
import LoadingState from '../components/LoadingState'
import EmptyState from '../components/EmptyState'
import SessionLogin from '../components/SessionLogin'
import { APIError } from '../api/client'

const PHASE_FILTERS = ['', 'Detected', 'AwaitingApproval', 'Executing', 'Verifying', 'Resolved', 'Escalated']
const SEVERITY_FILTERS = ['', 'critical', 'warning', 'info']

/** 格式化持续时间/MTTR */
function formatDuration(startedAt: string, resolvedAt?: string) {
  const start = new Date(startedAt).getTime()
  const end = resolvedAt ? new Date(resolvedAt).getTime() : Date.now()
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) {
    return '—'
  }
  const seconds = Math.round((end - start) / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  return `${minutes}m ${seconds % 60}s`
}

/** 事故总览页：紧凑应用栏、状态统计、过滤器与核心 Incident 表格。 */
function DashboardPage() {
  const [namespace, setNamespace] = useState('')
  const [phase, setPhase] = useState('')
  const [severity, setSeverity] = useState('')
  const { data, isLoading, isError, error, dataUpdatedAt } = useIncidents({
    namespace: namespace || undefined,
    phase: phase || undefined,
    severity: severity || undefined,
  })

  const cluster = data?.items?.[0]?.spec?.cluster ?? '未指定 (Unavailable)'

  const stats = {
    total: data?.items?.length ?? 0,
    active: data?.items?.filter((i) => !isTerminal(i.status.phase)).length ?? 0,
    awaiting: data?.items?.filter((i) => i.status.phase === 'AwaitingApproval').length ?? 0,
    resolved: data?.items?.filter((i) => i.status.phase === 'Resolved').length ?? 0,
    escalated: data?.items?.filter((i) => i.status.phase === 'Escalated').length ?? 0,
  }

  return (
    <div className="dashboard-container">
      {/* 顶部紧凑 64px 运维控制台导航栏 */}
      <header className="top-app-bar">
        <div className="app-bar-brand">
          <h1 className="brand-title">AegisOps 事故控制台</h1>
        </div>
        <div className="app-bar-meta">
          <span className="meta-tag cluster-tag">集群: {cluster}</span>
          <span className="meta-tag namespace-tag">命名空间: {namespace || '全部'}</span>
          <span className="meta-tag health-tag">
            <span className="health-dot" aria-hidden="true" />
            控制面在线 (Healthy)
          </span>
          <span className="meta-tag sync-tag">
            最后同步: {dataUpdatedAt ? new Date(dataUpdatedAt).toLocaleTimeString() : '—'}
          </span>
        </div>
      </header>

      <main className="dashboard-body">
        {/* 一行紧凑 KPI 状态摘要（高度 < 20%） */}
        <section className="kpi-summary-row" aria-label="事故状态摘要">
          <div className="kpi-card kpi-total">
            <span className="kpi-label">全部事故</span>
            <span className="kpi-value">{stats.total}</span>
          </div>
          <div className="kpi-card kpi-active">
            <span className="kpi-label">进行中</span>
            <span className="kpi-value">{stats.active}</span>
          </div>
          <div className="kpi-card kpi-awaiting">
            <span className="kpi-label">待审批 (Gate)</span>
            <span className="kpi-value">{stats.awaiting}</span>
          </div>
          <div className="kpi-card kpi-resolved">
            <span className="kpi-label">已恢复 (Resolved)</span>
            <span className="kpi-value">{stats.resolved}</span>
          </div>
          <div className="kpi-card kpi-escalated">
            <span className="kpi-label">已升级</span>
            <span className="kpi-value">{stats.escalated}</span>
          </div>
        </section>

        {/* 紧凑过滤器 */}
        <section className="filter-controls-row" aria-label="过滤器">
          <div className="filter-inputs">
            <input
              type="text"
              placeholder="按命名空间过滤..."
              value={namespace}
              onChange={(e) => setNamespace(e.target.value)}
              aria-label="按命名空间过滤"
              className="filter-input"
            />
            <select
              value={phase}
              onChange={(e) => setPhase(e.target.value)}
              aria-label="按阶段过滤"
              className="filter-select"
            >
              {PHASE_FILTERS.map((p) => (
                <option key={p} value={p}>
                  {p === '' ? '全部阶段' : p}
                </option>
              ))}
            </select>
            <select
              value={severity}
              onChange={(e) => setSeverity(e.target.value)}
              aria-label="按严重级别过滤"
              className="filter-select"
            >
              {SEVERITY_FILTERS.map((s) => (
                <option key={s} value={s}>
                  {s === '' ? '全部级别' : s}
                </option>
              ))}
            </select>
          </div>
        </section>

        {/* 核心主视觉：Incident 表格 */}
        <section className="incident-table-section">
          {isLoading && <LoadingState />}
          {isError && error instanceof APIError && error.status === 401 && (
            <SessionLogin onAuthenticated={() => window.location.reload()} />
          )}
          {isError && !(error instanceof APIError && error.status === 401) && (
            <div role="alert" className="error-state">
              加载失败: {error instanceof Error ? error.message : String(error)}
            </div>
          )}
          {data && (data.items?.length ?? 0) === 0 && <EmptyState message="当前集群暂无事故记录" />}
          {data && (data.items?.length ?? 0) > 0 && (
            <div className="table-responsive">
              <table className="incident-data-table">
                <thead>
                  <tr>
                    <th>事故名称</th>
                    <th>严重级别</th>
                    <th>目标工作负载</th>
                    <th>当前阶段</th>
                    <th>处置动作</th>
                    <th>持续时间</th>
                    <th>执行者/责任人</th>
                    <th>更新时间</th>
                  </tr>
                </thead>
                <tbody>
                  {data.items.map((i) => {
                    const durationStr = formatDuration(i.spec.startedAt, i.spec.resolvedAt)
                    const actor = i.status.approval?.actor ? `@${i.status.approval.actor}` : '—'

                    return (

                      <tr key={i.metadata.uid ?? i.metadata.name}>
                        <td className="cell-name">
                          <Link to={`/incidents/${i.metadata.namespace}/${i.metadata.name}`}>
                            {i.metadata.name}
                          </Link>
                        </td>
                        <td>
                          <span className={`badge badge-severity severity-${i.spec.severity}`}>
                            {i.spec.severity}
                          </span>
                        </td>
                        <td className="cell-target">
                          <code>{i.spec.targetRef.kind}/{i.spec.targetRef.name}</code>
                        </td>
                        <td>
                          <span className={`badge badge-phase phase-${i.status.phase}`}>
                            {i.status.phase ?? '—'}
                          </span>
                        </td>
                        <td className="cell-action">
                          {i.status.proposal?.action ? (
                            <span className="action-pill">{i.status.proposal.action}</span>
                          ) : (
                            <span className="text-muted">—</span>
                          )}
                        </td>
                        <td className="mono">{durationStr}</td>
                        <td className="cell-actor">
                          <span className="actor-text">{actor}</span>
                        </td>
                        <td className="mono text-muted">
                          {new Date(i.spec.lastReceivedAt ?? i.spec.startedAt).toLocaleString()}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
        </section>
      </main>
    </div>
  )
}

export default DashboardPage

