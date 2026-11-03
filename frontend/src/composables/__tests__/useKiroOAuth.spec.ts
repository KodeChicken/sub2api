import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { KiroOAuthResult, KiroOAuthStart } from '@/api/admin/kiro'

const api = vi.hoisted(() => ({
  start: vi.fn(), complete: vi.fn(), poll: vi.fn(), finalize: vi.fn(), status: vi.fn(), cancel: vi.fn()
}))
vi.mock('@/api/admin/kiro', () => ({ kiroAPI: api }))
import { useKiroOAuth } from '../useKiroOAuth'

const input: KiroOAuthStart = { intent: 'create', auth_type: 'social', account: { name: 'Kiro', concurrency: 10, priority: 1, group_ids: [] } }
const waiting = (id = 'session'): KiroOAuthResult => ({ session_id: id, auth_type: 'social', status: 'waiting', auth_url: 'https://app.kiro.dev/signin' })
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}
function setup() {
  let key = 'kiro:create:proxy-1'
  let oauth!: ReturnType<typeof useKiroOAuth>
  const success = vi.fn()
  const wrapper = mount(defineComponent({
    setup() { oauth = useKiroOAuth(() => key, success); return () => null }
  }))
  return { oauth, success, wrapper, setKey: (value: string) => { key = value } }
}

describe('useKiroOAuth lifecycle', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    Object.values(api).forEach(mock => mock.mockReset())
    api.start.mockResolvedValue(waiting())
    api.cancel.mockResolvedValue({ session_id: 'session', status: 'cancelled' })
  })
  afterEach(() => { vi.clearAllTimers(); vi.useRealTimers() })

  it('stages then finalizes once and never sends tokens through the browser', async () => {
    const { oauth, success, wrapper } = setup()
    api.complete.mockResolvedValue({ session_id: 'session', status: 'ready' })
    api.finalize.mockResolvedValue({ session_id: 'session', status: 'succeeded', account_id: 42 })
    await oauth.start(input)
    await oauth.complete(' http://127.0.0.1:3128/oauth/callback?code=c&state=s ')
    expect(api.complete).toHaveBeenCalledWith('session', 'http://127.0.0.1:3128/oauth/callback?code=c&state=s', expect.any(AbortSignal))
    expect(api.finalize.mock.calls).toEqual([['session']])
    expect(success.mock.calls).toEqual([[42]])
    api.status.mockResolvedValue({ session_id: 'session', status: 'succeeded', account_id: 42 })
    await oauth.check()
    expect(success).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('cancels a late start without replacing the newer session', async () => {
    const { oauth, wrapper } = setup()
    const old = deferred<KiroOAuthResult>()
    api.start.mockReturnValueOnce(old.promise).mockResolvedValueOnce(waiting('new'))
    const first = oauth.start(input)
    await flushPromises()
    await oauth.start(input)
    old.resolve(waiting('old'))
    await first
    expect(oauth.session.value?.session_id).toBe('new')
    expect(api.cancel).toHaveBeenCalledWith('old')
    expect(api.finalize).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('ignores an exchange after closing or switching the target', async () => {
    const { oauth, setKey, success, wrapper } = setup()
    const exchange = deferred<KiroOAuthResult>()
    api.complete.mockReturnValue(exchange.promise)
    await oauth.start(input)
    const completing = oauth.complete('callback')
    const signal = api.complete.mock.calls[0]?.[2] as AbortSignal
    setKey('kiro:reauth:other-account')
    await oauth.reset()
    expect(signal.aborted).toBe(true)
    exchange.resolve({ session_id: 'session', status: 'ready' })
    await completing
    expect(api.finalize).not.toHaveBeenCalled()
    expect(success).not.toHaveBeenCalled()
    expect(oauth.session.value).toBeNull()
    wrapper.unmount()
  })

  it('uses one recursive device timer and stops it on reset', async () => {
    const { oauth, wrapper } = setup()
    api.start.mockResolvedValue({ session_id: 'device', auth_type: 'builder_id', status: 'waiting', retry_after: 5 })
    api.poll.mockResolvedValue({ session_id: 'device', auth_type: 'builder_id', status: 'waiting', retry_after: 15 })
    await oauth.start({ ...input, auth_type: 'builder_id' })
    await vi.advanceTimersByTimeAsync(4999)
    expect(api.poll).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(api.poll).toHaveBeenCalledOnce()
    await vi.advanceTimersByTimeAsync(14999)
    expect(api.poll).toHaveBeenCalledOnce()
    await vi.advanceTimersByTimeAsync(1)
    expect(api.poll).toHaveBeenCalledTimes(2)
    await oauth.reset()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.poll).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('does not finalize a device result arriving after cancellation', async () => {
    const { oauth, wrapper } = setup()
    const poll = deferred<KiroOAuthResult>()
    api.start.mockResolvedValue({ session_id: 'device', auth_type: 'iam_sso', status: 'waiting', retry_after: 5 })
    api.poll.mockReturnValue(poll.promise)
    await oauth.start({ ...input, auth_type: 'iam_sso' })
    await vi.advanceTimersByTimeAsync(5000)
    await oauth.reset()
    poll.resolve({ session_id: 'device', status: 'ready' })
    await flushPromises()
    expect(api.finalize).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('retains a failed cancellation identity and requires cancellation before restarting', async () => {
    const { oauth, wrapper } = setup()
    await oauth.start(input)
    api.cancel.mockRejectedValueOnce({ reason: 'KIRO_OAUTH_SESSION_STORE_UNAVAILABLE' })
    expect(await oauth.reset()).toBe(false)
    expect(oauth.session.value?.session_id).toBe('session')
    expect(oauth.error.value).toBe('CANCEL_FAILED')
    await oauth.start(input)
    expect(api.cancel).toHaveBeenCalledTimes(2)
    expect(api.start).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('reconciles an uncertain save and requires an explicit retry after recovery', async () => {
    const { oauth, success, wrapper } = setup()
    api.start.mockResolvedValue({ session_id: 'session', status: 'ready' })
    api.finalize.mockRejectedValueOnce({ reason: 'KIRO_OAUTH_PERSISTENCE_UNKNOWN' })
    api.status.mockResolvedValue({ session_id: 'session', status: 'ready', error_code: 'SAVE_FAILED' })
    await oauth.start({ ...input, auth_type: 'credentials', credentials: { refresh_token: 'new' } })
    expect(oauth.saving.value).toBe(true)
    expect(oauth.error.value).toBe('PERSISTENCE_UNKNOWN')
    expect(await oauth.reset()).toBe(false)
    await vi.advanceTimersByTimeAsync(2000)
    expect(oauth.session.value?.status).toBe('ready')
    expect(api.finalize).toHaveBeenCalledOnce()
    expect(success).not.toHaveBeenCalled()
    api.finalize.mockResolvedValue({ session_id: 'session', status: 'succeeded', account_id: 42 })
    await oauth.finalize()
    expect(success.mock.calls).toEqual([[42]])
    expect(api.complete).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('does not restore a stale cancelled session after the binding changes', async () => {
    const { oauth, setKey, wrapper } = setup()
    await oauth.start(input)
    const cancel = deferred<KiroOAuthResult>()
    api.cancel.mockReturnValueOnce(cancel.promise)
    const reset = oauth.reset()
    setKey('other-target')
    cancel.resolve({ session_id: 'session', status: 'succeeded', account_id: 42 })
    expect(await reset).toBe(false)
    expect(oauth.session.value).toBeNull()
    wrapper.unmount()
  })

  it('honors a committed receipt discovered during cancellation without starting another account', async () => {
    const { oauth, success, wrapper } = setup()
    await oauth.start(input)
    api.cancel.mockResolvedValue({ session_id: 'session', status: 'succeeded', account_id: 42 })
    await oauth.start(input)
    expect(api.start).toHaveBeenCalledOnce()
    expect(success.mock.calls).toEqual([[42]])
    expect(oauth.session.value?.status).toBe('succeeded')
    expect(await oauth.reset()).toBe(true)
    expect(api.cancel).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('stops timers on unmount and does not claim cancellation of an accepted save', async () => {
    const { oauth, success, wrapper } = setup()
    const saving = deferred<KiroOAuthResult>()
    api.start.mockResolvedValue({ session_id: 'session', status: 'ready' })
    api.finalize.mockReturnValue(saving.promise)
    const started = oauth.start(input)
    await flushPromises()
    expect(oauth.saving.value).toBe(true)
    wrapper.unmount()
    saving.resolve({ session_id: 'session', status: 'succeeded', account_id: 42 })
    await started
    expect(api.cancel).not.toHaveBeenCalled()
    expect(success).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })
})
