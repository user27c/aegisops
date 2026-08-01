import { Route, Routes } from 'react-router-dom'
import DashboardPage from './pages/DashboardPage'
import IncidentDetailPage from './pages/IncidentDetailPage'
import NotFoundPage from './pages/NotFoundPage'

/** 应用根组件：路由与全局布局。 */
function App() {
  return (
    <Routes>
      <Route path="/" element={<DashboardPage />} />
      <Route path="/incidents/:namespace/:name" element={<IncidentDetailPage />} />
      <Route path="*" element={<NotFoundPage />} />
    </Routes>
  )
}

export default App
