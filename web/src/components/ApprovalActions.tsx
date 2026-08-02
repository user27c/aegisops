import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { apiFetch, APIError } from '../api/client'
import type { AIOpsIncident } from '../api/types'

interface ApprovalActionsProps {
  incident: AIOpsIncident
}

/** 审批操作区：仅 AwaitingApproval 阶段显示。 */
function ApprovalActions({ incident }: ApprovalActionsProps) {
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<string | null>(null)
  const queryClient = useQueryClient()

  const phase = incident.status.phase
  if (phase !== 'AwaitingApproval') {
    return null
  }

  const submit = async (decision: 'Approve' | 'Reject') => {
    if (reason.trim().length < 4) {
      setError('审批理由至少 4 个字符')
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      await apiFetch(
        `/incidents/${incident.metadata.namespace}/${incident.metadata.name}/approval`,
        {
          method: 'POST',
          body: JSON.stringify({ decision, reason: reason.trim() }),
        },
      )
      setDone(decision === 'Approve' ? '已批准' : '已拒绝')
      setReason('')
      // 刷新详情。
      await queryClient.invalidateQueries({
        queryKey: ['incident', incident.metadata.namespace, incident.metadata.name],
      })
    } catch (e) {
      setError(e instanceof APIError ? e.message : String(e))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <section className="approval-actions card" aria-label="审批操作">
      <h2>人工审批</h2>
      {done && <p className="approval-done">{done}，等待 Operator 执行。</p>}
      <textarea
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        placeholder="审批理由（至少 4 个字符）"
        aria-label="审批理由"
        rows={2}
        disabled={submitting || Boolean(done)}
      />
      {error && (
        <p role="alert" className="error-state">
          {error}
        </p>
      )}
      <div className="approval-buttons">
        <button
          type="button"
          className="btn-approve"
          onClick={() => submit('Approve')}
          disabled={submitting || Boolean(done)}
        >
          批准并执行
        </button>
        <button
          type="button"
          className="btn-reject"
          onClick={() => submit('Reject')}
          disabled={submitting || Boolean(done)}
        >
          拒绝
        </button>
      </div>
      <p className="approval-hint">方案摘要: {incident.status.proposal?.planDigest?.slice(0, 24)}…</p>
    </section>
  )
}

export default ApprovalActions
