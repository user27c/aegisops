import { describe, expect, it } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { server } from '../test/server'
import ApprovalActions from './ApprovalActions'
import { incidentOOM } from '../test/fixtures'

function renderApproval(incident = incidentOOM) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={qc}>
      <ApprovalActions incident={incident} />
    </QueryClientProvider>,
  )
}

describe('ApprovalActions', () => {
  it('非待审批阶段不渲染', () => {
    const resolved = { ...incidentOOM, status: { ...incidentOOM.status, phase: 'Resolved' as const } }
    const { container } = renderApproval(resolved)
    expect(container).toBeEmptyDOMElement()
  })

  it('待审批阶段显示操作区', () => {
    renderApproval()
    expect(screen.getByLabelText('审批理由')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '批准并执行' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '拒绝' })).toBeInTheDocument()
  })

  it('理由过短拒绝提交', async () => {
    const user = userEvent.setup()
    renderApproval()
    await user.type(screen.getByLabelText('审批理由'), 'ok')
    await user.click(screen.getByRole('button', { name: '批准并执行' }))
    expect(screen.getByRole('alert')).toHaveTextContent('至少 4 个字符')
  })

  it('批准成功显示完成提示', async () => {
    server.use(
      http.post('/api/v1/incidents/fault-lab/containeroomkilled-abc123/approval', () => {
        return HttpResponse.json({ approvalName: 'a-1', decision: 'Approve' }, { status: 201 })
      }),
    )
    const user = userEvent.setup()
    renderApproval()
    await user.type(screen.getByLabelText('审批理由'), '确认修复方案')
    await user.click(screen.getByRole('button', { name: '批准并执行' }))
    await waitFor(() => {
      expect(screen.getByText('已批准，等待 Operator 执行。')).toBeInTheDocument()
    })
  })

  it('审批失败显示错误', async () => {
    server.use(
      http.post('/api/v1/incidents/fault-lab/containeroomkilled-abc123/approval', () => {
        return HttpResponse.json(
          { code: 'PHASE_CONFLICT', message: '事故当前处于 Executing，不在待审批阶段' },
          { status: 409 },
        )
      }),
    )
    const user = userEvent.setup()
    renderApproval()
    await user.type(screen.getByLabelText('审批理由'), '确认修复方案')
    await user.click(screen.getByRole('button', { name: '批准并执行' }))
    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('不在待审批阶段')
    })
  })
})
