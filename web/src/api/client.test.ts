import { describe, expect, it } from 'vitest'
import { APIError, apiFetch } from '../api/client'

describe('api client', () => {
  it('APIError 保留状态码与错误码', () => {
    const err = new APIError(409, 'PHASE_CONFLICT', '阶段冲突')
    expect(err.status).toBe(409)
    expect(err.code).toBe('PHASE_CONFLICT')
    expect(err.message).toBe('阶段冲突')
    expect(err).toBeInstanceOf(Error)
  })

  it('apiFetch 在非 2xx 时抛出 APIError', async () => {
    const originalFetch = globalThis.fetch
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ code: 'NOT_FOUND', message: '不存在' }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      })
    try {
      await expect(apiFetch('/incidents/x/y')).rejects.toMatchObject({
        status: 404,
        code: 'NOT_FOUND',
      })
    } finally {
      globalThis.fetch = originalFetch
    }
  })

  it('apiFetch 成功时解析 JSON', async () => {
    const originalFetch = globalThis.fetch
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ items: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    try {
      const body = await apiFetch<{ items: unknown[] }>('/incidents')
      expect(body.items).toEqual([])
    } finally {
      globalThis.fetch = originalFetch
    }
  })
})
