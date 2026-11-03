import { beforeEach, describe, expect, it, vi } from 'vitest'
const client = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
import { kiroAPI } from '@/api/admin/kiro'

describe('Kiro OAuth API contract', () => {
  beforeEach(() => Object.values(client).forEach(mock => mock.mockReset().mockResolvedValue({ data: { status: 'waiting' } })))

  it('sends immutable settings only at start and the whole callback only at completion', async () => {
    const input = { intent: 'reauth' as const, auth_type: 'iam_sso' as const, account_id: 42, start_url: 'https://company.awsapps.com/start' }
    await kiroAPI.start(input)
    expect(client.post).toHaveBeenLastCalledWith('/admin/kiro/oauth/sessions', input, { timeout: 30_000 })
    const callback = 'http://127.0.0.1:3128/signin/callback?code=c&state=s&login_option=GitHub'
    await kiroAPI.complete('a/b', callback)
    expect(client.post).toHaveBeenLastCalledWith('/admin/kiro/oauth/sessions/a%2Fb/complete', { callback_url: callback }, { timeout: 30_000 })
  })

  it('has no mutable account, proxy or token payload on action endpoints', async () => {
    await kiroAPI.poll('id')
    expect(client.post).toHaveBeenLastCalledWith('/admin/kiro/oauth/sessions/id/poll', undefined, { timeout: 30_000 })
    await kiroAPI.finalize('id')
    expect(client.post).toHaveBeenLastCalledWith('/admin/kiro/oauth/sessions/id/finalize', undefined, { timeout: 30_000 })
    await kiroAPI.status('id')
    expect(client.get).toHaveBeenLastCalledWith('/admin/kiro/oauth/sessions/id', { timeout: 30_000 })
    await kiroAPI.cancel('id')
    expect(client.delete).toHaveBeenLastCalledWith('/admin/kiro/oauth/sessions/id', { timeout: 30_000 })
  })

  it('can abort noncommitting requests without changing callback or account bindings', async () => {
    const controller = new AbortController()
    await kiroAPI.complete('id', 'complete-callback', controller.signal)
    expect(client.post).toHaveBeenLastCalledWith('/admin/kiro/oauth/sessions/id/complete', { callback_url: 'complete-callback' }, { timeout: 30_000, signal: controller.signal })
    await kiroAPI.poll('id', controller.signal)
    expect(client.post).toHaveBeenLastCalledWith('/admin/kiro/oauth/sessions/id/poll', undefined, { timeout: 30_000, signal: controller.signal })
  })
})
