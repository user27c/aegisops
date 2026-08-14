import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import PhaseStepper from '../components/PhaseStepper'

describe('PhaseStepper', () => {
  it('渲染正常流程阶段且不包含 RollingBack', () => {
    render(<PhaseStepper phase="AwaitingApproval" />)
    expect(screen.getByLabelText('处置阶段流水线')).toBeInTheDocument()
    expect(screen.getByText('AwaitingApproval')).toBeInTheDocument()
    expect(screen.getByText('Detected')).toBeInTheDocument()
    expect(screen.getByText('Resolved')).toBeInTheDocument()
    expect(screen.queryByText('RollingBack')).not.toBeInTheDocument()
  })

  it('成功路径 Resolved 渲染全部 8 个步骤且无 RollingBack', () => {
    render(<PhaseStepper phase="Resolved" />)
    expect(screen.getByText('Resolved')).toBeInTheDocument()
    expect(screen.queryByText('RollingBack')).not.toBeInTheDocument()
  })

  it('发生回滚时展示 RollingBack 异常分支步骤', () => {
    render(<PhaseStepper phase="RollingBack" />)
    expect(screen.getByText('RollingBack')).toBeInTheDocument()
    expect(screen.getByText('RolledBack')).toBeInTheDocument()
  })

  it('无阶段时不渲染', () => {
    const { container } = render(<PhaseStepper phase={undefined} />)
    expect(container).toBeEmptyDOMElement()
  })
})

