/** 加载状态。 */
function LoadingState({ label = '加载中…' }: { label?: string }) {
  return (
    <div className="loading-state" role="status">
      {label}
    </div>
  )
}

export default LoadingState
