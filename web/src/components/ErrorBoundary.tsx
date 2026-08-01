import { Component, type ErrorInfo, type ReactNode } from 'react'

interface Props {
  children: ReactNode
}

interface State {
  hasError: boolean
}

/** 错误边界：渲染错误时不白屏。 */
class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false }

  static getDerivedStateFromError(): State {
    return { hasError: true }
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // M7 接入前端错误上报。
    console.error('渲染错误:', error, info.componentStack)
  }

  render(): ReactNode {
    if (this.state.hasError) {
      return (
        <div className="error-boundary" role="alert">
          <h2>页面出错了</h2>
          <p>请刷新重试；若持续出现请联系管理员。</p>
          <button type="button" onClick={() => window.location.reload()}>
            刷新
          </button>
        </div>
      )
    }
    return this.props.children
  }
}

export default ErrorBoundary
