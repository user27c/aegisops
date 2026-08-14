import type { IncidentPhase } from '../api/types'

/** 状态机线性主路径（不含回滚异常分支） */
const HAPPY_PATH: IncidentPhase[] = [
  'Detected',
  'CollectingEvidence',
  'Diagnosing',
  'PolicyChecking',
  'AwaitingApproval',
  'Executing',
  'Verifying',
  'Resolved',
]

/** 阶段步骤条：展示当前阶段与完成状态，回滚作为异常分支处理，不在成功路径中展示。 */
function PhaseStepper({ phase }: { phase: IncidentPhase | undefined }) {
  if (!phase) return null

  let steps: IncidentPhase[]
  if (phase === 'RollingBack' || phase === 'RolledBack') {
    steps = [
      'Detected',
      'CollectingEvidence',
      'Diagnosing',
      'PolicyChecking',
      'AwaitingApproval',
      'Executing',
      'Verifying',
      'RollingBack',
      'RolledBack',
    ]
  } else if (phase === 'Escalated') {
    steps = [
      'Detected',
      'CollectingEvidence',
      'Diagnosing',
      'PolicyChecking',
      'AwaitingApproval',
      'Escalated',
    ]
  } else {
    steps = HAPPY_PATH
  }

  const currentIndex = steps.indexOf(phase)
  const isResolved = phase === 'Resolved'

  return (
    <nav className="phase-stepper-container" aria-label="处置阶段流水线">
      <ol className="phase-stepper">
        {steps.map((step, idx) => {
          const isCurrent = step === phase
          const isDone = isResolved || (currentIndex >= 0 && idx < currentIndex)

          let stateClass = ''
          if (isCurrent) stateClass = 'current'
          else if (isDone) stateClass = 'done'
          else stateClass = 'pending'

          return (
            <li key={step} className={`phase-step ${stateClass}`}>
              <div className="phase-step-marker">
                <span className="phase-step-dot" aria-hidden="true" />
                {idx < steps.length - 1 && <span className="phase-step-line" aria-hidden="true" />}
              </div>
              <span className="phase-step-label">{step}</span>
            </li>
          )
        })}
      </ol>
    </nav>
  )
}

export default PhaseStepper

