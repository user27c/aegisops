import { useIncidents } from '../hooks/useIncidents'

/** 事故总览页：状态统计、过滤器、Incident 表格（M1 完善）。 */
function DashboardPage() {
  const { data, isLoading, isError, error } = useIncidents()

  return (
    <main style={{ padding: '24px' }}>
      <h1>AegisOps 事故控制台</h1>
      {isLoading && <p>加载中…</p>}
      {isError && <p role="alert">加载失败: {String(error)}</p>}
      {data && (
        <table>
          <thead>
            <tr>
              <th>名称</th>
              <th>严重级别</th>
              <th>目标</th>
              <th>阶段</th>
            </tr>
          </thead>
          <tbody>
            {data.items.map((i) => (
              <tr key={i.metadata.uid ?? i.metadata.name}>
                <td>{i.metadata.name}</td>
                <td>{i.spec.severity}</td>
                <td>
                  {i.spec.targetRef.kind}/{i.spec.targetRef.name}
                </td>
                <td>{i.status.phase}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </main>
  )
}

export default DashboardPage
