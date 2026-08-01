/** API 客户端：统一错误处理、AbortSignal、Authorization。 */

const API_BASE = import.meta.env.VITE_API_BASE ?? '/api/v1'

export class APIError extends Error {
  status: number
  code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'APIError'
    this.status = status
    this.code = code
  }
}

/** 通用请求封装。Token 只放 sessionStorage，不长期落盘。 */
export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  const token = sessionStorage.getItem('aegisops_token')
  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  const resp = await fetch(`${API_BASE}${path}`, { ...init, headers })
  if (!resp.ok) {
    let code = 'UNKNOWN'
    let message = `请求失败: ${resp.status}`
    try {
      const body = (await resp.json()) as { code?: string; message?: string }
      code = body.code ?? code
      message = body.message ?? message
    } catch {
      // 非 JSON 响应，保留默认消息
    }
    throw new APIError(resp.status, code, message)
  }
  if (resp.status === 204) {
    return undefined as T
  }
  return (await resp.json()) as T
}
