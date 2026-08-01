import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import PhaseStepper from '../components/PhaseStepper'

describe('PhaseStepper', () => {
  it('渲染当前阶段', () => {
    render(<PhaseStepper phase="AwaitingApproval" />)
    expect(screen.getByLabelText('事故阶段')).toBeInTheDocument()
    expect(screen.getByText('AwaitingApproval')).toBeInTheDocument()
    expect(screen.getByText('Detected')).toBeInTheDocument()
  })

  it('终态显示终态徽章', () => {
    render(<PhaseStepper phase="Resolved" />)
    expect(screen.getAllByText('Resolved').length).toBeGreaterThanOrEqual(1)
  })

  it('无阶段时不渲染', () => {
    const { container } = render(<PhaseStepper phase={undefined} />)
    expect(container).toBeEmptyDOMElement()
  })
})
