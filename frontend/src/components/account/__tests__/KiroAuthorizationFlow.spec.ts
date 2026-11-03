import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  start: vi.fn(), complete: vi.fn(), poll: vi.fn(), finalize: vi.fn(), status: vi.fn(), cancel: vi.fn()
}))
vi.mock('@/api/admin/kiro', () => ({ kiroAPI: api }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, te: () => true })
}))
import KiroAuthorizationFlow from '../KiroAuthorizationFlow.vue'

const draft = { name: 'Kiro', concurrency: 10, priority: 1, group_ids: [5], proxy_id: 8 }
const mounted: ReturnType<typeof mount>[] = []
const render = () => {
  const wrapper = mount(KiroAuthorizationFlow, {
    props: { draft, configuration: { temp_unschedulable_enabled: false } },
    global: { stubs: { Icon: true, ModelWhitelistSelector: true } }
  })
  mounted.push(wrapper)
  return wrapper
}
const button = (wrapper: ReturnType<typeof render>, key: string) => wrapper.findAll('button').find(item => item.text().includes(key))!

describe('KiroAuthorizationFlow', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    Object.values(api).forEach(mock => mock.mockReset())
    api.start.mockResolvedValue({ session_id: 's', auth_type: 'social', status: 'waiting', auth_url: 'https://app.kiro.dev/signin?state=secret' })
    api.cancel.mockResolvedValue({ session_id: 's', status: 'cancelled' })
  })
  afterEach(() => { mounted.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllTimers(); vi.useRealTimers() })

  it('defaults to portal sign-in, hides credentials and sends the complete callback URL', async () => {
    const wrapper = render()
    expect(wrapper.get<HTMLInputElement>('input[value="signin"]').element.checked).toBe(true)
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
    await button(wrapper, 'generateAuthUrl').trigger('click')
    await flushPromises()
    expect(api.start).toHaveBeenCalledWith(expect.objectContaining({
      intent: 'create', auth_type: 'social', account: draft,
      settings: { endpoint: 'ide', api_region: 'us-east-1', api_domain: 'q', temp_unschedulable_enabled: false }
    }))
    const link = wrapper.get('a[target="_blank"]')
    expect(link.attributes('rel')).toBe('noopener noreferrer')
    const callback = 'http://127.0.0.1:3128/signin/callback?code=complete&state=s&login_option=GitHub'
    await wrapper.get('textarea').setValue(callback)
    api.complete.mockResolvedValue({ session_id: 's', status: 'ready' })
    api.finalize.mockResolvedValue({ session_id: 's', status: 'succeeded', account_id: 42 })
    await button(wrapper, 'completeAuth').trigger('click')
    await flushPromises()
    expect(api.complete).toHaveBeenCalledWith('s', callback, expect.any(AbortSignal))
    expect(wrapper.emitted('authorized')).toEqual([[42]])
  })

  it('shows enterprise Start URL and device user code with server-driven polling', async () => {
    const wrapper = render()
    await wrapper.get('input[value="iam_sso"]').setValue()
    await wrapper.get('input[type="url"]').setValue('https://company.awsapps.com/start')
    api.start.mockResolvedValue({ session_id: 'd', auth_type: 'iam_sso', status: 'waiting', user_code: 'ABCD-EFGH', verification_uri: 'https://device.example/start', retry_after: 15 })
    await button(wrapper, 'generateAuthUrl').trigger('click')
    await flushPromises()
    expect(api.start.mock.calls[0]?.[0]).toMatchObject({ auth_type: 'iam_sso', start_url: 'https://company.awsapps.com/start' })
    expect(wrapper.text()).toContain('ABCD-EFGH')
    expect(wrapper.find('textarea').exists()).toBe(false)
    api.poll.mockResolvedValue({ session_id: 'd', status: 'denied', error_code: 'ACCESS_DENIED' })
    await vi.advanceTimersByTimeAsync(15_000)
    await flushPromises()
    expect(wrapper.text()).toContain('state.denied')
    expect(wrapper.get('[role="alert"]').text()).toContain('ACCESS_DENIED')
    expect(api.finalize).not.toHaveBeenCalled()
  })

  it('preserves imported CLI regions and settings without applying normal sign-in defaults', async () => {
    const wrapper = render()
    await wrapper.get('input[value="import"]').setValue()
    await wrapper.get('textarea').setValue(JSON.stringify({
      refreshToken: 'imported', endpoint: 'cli', apiRegion: 'eu-west-1', apiDomain: 'runtime'
    }))
    api.start.mockResolvedValue({ session_id: 'i', status: 'ready' })
    api.finalize.mockResolvedValue({ session_id: 'i', status: 'succeeded', account_id: 42 })
    await button(wrapper, 'completeAuth').trigger('click')
    await flushPromises()
    expect(api.start.mock.calls[0]?.[0]).toMatchObject({
      auth_type: 'credentials',
      credentials: { refresh_token: 'imported', endpoint: 'cli', api_region: 'eu-west-1', api_domain: 'runtime' },
      settings: { temp_unschedulable_enabled: false }
    })
    expect(wrapper.text()).toContain('entraImportOnly')
  })

  it('keeps manual and Entra alternatives and invalidates an existing portal session on mode change', async () => {
    const wrapper = render()
    await button(wrapper, 'generateAuthUrl').trigger('click')
    await flushPromises()
    await wrapper.get('input[value="manual"]').setValue()
    await flushPromises()
    expect(api.cancel).toHaveBeenCalledWith('s')
    expect(wrapper.find('[data-testid="kiro-credentials"]').exists()).toBe(true)
    expect(wrapper.find('option[value="external_idp"]').text()).toContain('entraImportOnly')
  })
})
