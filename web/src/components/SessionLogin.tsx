import { useState } from 'react'

interface SessionLoginProps {
  onAuthenticated: () => void
}

/** 会话级登录：Token 只保存在当前标签页，关闭标签页后自动清除。 */
function SessionLogin({ onAuthenticated }: SessionLoginProps) {
  const [token, setToken] = useState('')

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const value = token.trim()
    if (!value) return
    sessionStorage.setItem('aegisops_token', value)
    setToken('')
    onAuthenticated()
  }

  return (
    <section className="session-login" aria-label="控制台登录">
      <div className="login-shield" aria-hidden="true">A</div>
      <div>
        <span className="dialog-kicker">SESSION AUTHENTICATION</span>
        <h2>连接受保护的事故控制面</h2>
        <p>输入 Viewer 或 Approver Token。凭据只保存在当前浏览器标签页，不写入服务端日志或长期存储。</p>
      </div>
      <form onSubmit={submit}>
        <label htmlFor="console-token">Access Token</label>
        <div className="login-control">
          <input
            id="console-token"
            type="password"
            value={token}
            onChange={(event) => setToken(event.target.value)}
            autoComplete="off"
            placeholder="粘贴会话 Token"
          />
          <button type="submit" disabled={!token.trim()}>进入控制台</button>
        </div>
      </form>
    </section>
  )
}

export default SessionLogin
