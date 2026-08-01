/** 空状态。 */
function EmptyState({ message = '暂无数据' }: { message?: string }) {
  return <div className="empty-state">{message}</div>
}

export default EmptyState
