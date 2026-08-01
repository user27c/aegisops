import { useParams } from 'react-router-dom'
import { useIncident } from '../hooks/useIncident'

/** 单事故详情页（M1/M4 完善：证据、方案 Diff、审批）。 */
function IncidentDetailPage() {
  const { namespace = '', name = '' } = useParams()
  const { data, isLoading, isError } = useIncident(namespace, name)

  return (
    <main style={{ padding: '24px' }}>
      <h1>
        事故 {namespace}/{name}
      </h1>
      {isLoading && <p>加载中…</p>}
      {isError && <p role="alert">加载失败</p>}
      {data && <pre>{JSON.stringify(data.status, null, 2)}</pre>}
    </main>
  )
}

export default IncidentDetailPage
