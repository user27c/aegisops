import type { IncidentPhase } from '../api/types'

/** 状态机阶段定义：只按后端 Phase 显示，不在前端推断。 */
const PHASE_ORDER: IncidentPhase[] = [
  'Detected',
  'CollectingEvidence',
  'Diagnosing',
  'PolicyChecking',
  'AwaitingApproval',
  'Executing',
  'Verifying',
  'RollingBack',
  'Resolved',
  'RolledBack',
  'Escalated',
]

const TERMINAL_PHASES = new Set<IncidentPhase>(['Resolved', 'RolledBack', 'Escalated'])

/** 阶段步骤条：展示当前阶段与完成状态。 */
function PhaseStepper({ phase }: { phase: IncidentPhase | undefined }) {
  if (!phase) return null
  const currentIndex = PHASE_ORDER.indexOf(phase)
  const terminal = TERMINAL_PHASES.has(phase)

  return (
    <ol className="phase-stepper" aria-label="事故阶段">
      {PHASE_ORDER.slice(0, 8).map((p, idx) => {
        const isCurrent = p === phase
        const isDone = !terminal && currentIndex >= 0 && idx < currentIndex
        return (
          <li
            key={p}
            className={[
              'phase-step',
              isCurrent ? 'current' : '',
              isDone ? 'done' : '',
              terminal && isCurrent ? 'terminal' : '',
            ].join(' ')}
          >
            <span className="phase-step-dot" aria-hidden="true" />
            <span className="phase-step-label">{p}</span>
          </li>
        )
      })}
      {terminal && (
        <li className="phase-step terminal current">
          <span className="phase-step-dot" aria-hidden="true" />
          <span className="phase-step-label">{phase}</span>
        </li>
      )}
    </ol>
  )
}

export default PhaseStepper
