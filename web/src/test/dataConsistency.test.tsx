import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import PhaseStepper from '../components/PhaseStepper'
import PolicyDecisionCard from '../components/PolicyDecisionCard'
import ExecutionCard from '../components/ExecutionCard'
import type { AIOpsIncident } from '../api/types'

describe('P0 数据一致性与架构不变量验证 (Strict Truthfulness)', () => {
  it('1. 成功路径与 Resolved 状态下 PhaseStepper 不得出现 RollingBack 节点', () => {
    const { rerender } = render(<PhaseStepper phase="Resolved" />)
    expect(screen.getByText('Resolved')).toBeInTheDocument()
    expect(screen.queryByText('RollingBack')).not.toBeInTheDocument()

    rerender(<PhaseStepper phase="AwaitingApproval" />)
    expect(screen.getByText('AwaitingApproval')).toBeInTheDocument()
    expect(screen.queryByText('RollingBack')).not.toBeInTheDocument()
  })

  it('2. Medium Risk + Approval 场景下，决策必须严格基于 policyDecision，未提供时不胡乱推断', () => {
    const mediumRiskIncident: AIOpsIncident = {
      metadata: { name: 'oom-123', namespace: 'fault-lab' },
      spec: {
        fingerprint: 'sha256:1111',
        cluster: 'local-k3s',
        alertName: 'ContainerOOMKilled',
        severity: 'critical',
        sourceStatus: 'firing',
        targetRef: { apiVersion: 'apps/v1', kind: 'Deployment', namespace: 'fault-lab', name: 'faultlab' },
        startedAt: '2026-08-14T10:00:00Z',
      },
      status: {
        phase: 'Resolved',
        proposal: {
          revision: 1,
          action: 'PatchResourceLimit',
          risk: 'medium',
          planDigest: 'sha256:2222',
        },
        policyDecision: {
          decision: 'ApprovalRequired',
          reasonCodes: ['POLICY_APPROVAL_MISSING'],
        },
        approval: {
          decision: 'Approve',
          actor: 'token-admin',
        },
      },
    }

    render(<PolicyDecisionCard incident={mediumRiskIncident} />)
    expect(screen.getByText('ApprovalRequired')).toBeInTheDocument()
    expect(screen.queryByText('Auto')).not.toBeInTheDocument()
  })

  it('3. Execution 与快照数据缺失时，必须如实显示 Unavailable / 未提供，严禁伪造快照 ID', () => {
    const awaitingIncident: AIOpsIncident = {
      metadata: { name: 'oom-456', namespace: 'fault-lab' },
      spec: {
        fingerprint: 'sha256:3333',
        cluster: 'local-k3s',
        alertName: 'ContainerOOMKilled',
        severity: 'critical',
        sourceStatus: 'firing',
        targetRef: { apiVersion: 'apps/v1', kind: 'Deployment', namespace: 'fault-lab', name: 'faultlab' },
        startedAt: '2026-08-14T10:00:00Z',
      },
      status: {
        phase: 'AwaitingApproval',
        proposal: {
          revision: 1,
          action: 'PatchResourceLimit',
          parameters: { container: 'faultlab', memoryLimit: '384Mi' },
          risk: 'medium',
          planDigest: 'sha256:4444',
        },
      },
    }

    render(<ExecutionCard incident={awaitingIncident} />)
    expect(screen.getByText('1. Preflight')).toBeInTheDocument()
    expect(screen.getByText('2. Snapshot')).toBeInTheDocument()
    expect(screen.getByText('3. Apply')).toBeInTheDocument()
    expect(screen.getByText('4. Verify')).toBeInTheDocument()
    expect(screen.getByText('未提供快照 (Unavailable)')).toBeInTheDocument()
    expect(screen.queryByText(/snap-faultlab-256mi/)).not.toBeInTheDocument()
  })
})
