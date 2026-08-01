import { describe, expect, it } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import DashboardPage from '../pages/DashboardPage'

function renderDashboard() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <DashboardPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('DashboardPage', () => {
  it('渲染事故列表与统计', async () => {
    renderDashboard()
    expect(screen.getByRole('heading', { name: 'AegisOps 事故控制台' })).toBeInTheDocument()

    await waitFor(() => {
      expect(screen.getByText('containeroomkilled-abc123')).toBeInTheDocument()
    })
    expect(screen.getByText('containercrashloop-def456')).toBeInTheDocument()
    expect(screen.getAllByText('2').length).toBeGreaterThan(0) // 全部统计
  })

  it('显示阶段与严重级别徽章', async () => {
    renderDashboard()
    await waitFor(() => {
      expect(screen.getByText('AwaitingApproval')).toBeInTheDocument()
    })
    expect(screen.getByText('critical')).toBeInTheDocument()
    expect(screen.getByText('Resolved')).toBeInTheDocument()
  })

  it('过滤控件存在', () => {
    renderDashboard()
    expect(screen.getByLabelText('按命名空间过滤')).toBeInTheDocument()
    expect(screen.getByLabelText('按阶段过滤')).toBeInTheDocument()
    expect(screen.getByLabelText('按严重级别过滤')).toBeInTheDocument()
  })
})
