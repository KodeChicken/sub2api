import { defineComponent, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account } from '@/types'

const mocks = vi.hoisted(() => ({
  getById: vi.fn(), apply: vi.fn(), success: vi.fn(), generate: vi.fn(), reset: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getById: mocks.getById, applyOAuthCredentials: mocks.apply } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.success, showError: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@/composables/useAccountOAuth', async () => {
  const { ref } = await import('vue')
  return { useAccountOAuth: () => ({
    authUrl: ref(''), sessionId: ref(''), loading: ref(false), error: ref(''),
    resetState: vi.fn(), generateAuthUrl: mocks.generate
  }) }
})
vi.mock('@/composables/useOpenAIOAuth', async () => {
  const { ref } = await import('vue')
  return { useOpenAIOAuth: () => ({
    authUrl: ref(''), sessionId: ref(''), loading: ref(false), error: ref(''), resetState: vi.fn(), generateAuthUrl: mocks.generate
  }) }
})
vi.mock('@/composables/useGeminiOAuth', async () => {
  const { ref } = await import('vue')
  return { useGeminiOAuth: () => ({
    authUrl: ref(''), sessionId: ref(''), loading: ref(false), error: ref(''), resetState: vi.fn()
  }) }
})
vi.mock('@/composables/useAntigravityOAuth', async () => {
  const { ref } = await import('vue')
  return { useAntigravityOAuth: () => ({
    authUrl: ref(''), sessionId: ref(''), loading: ref(false), error: ref(''), resetState: vi.fn()
  }) }
})
vi.mock('@/composables/useGrokOAuth', async () => {
  const { ref } = await import('vue')
  return { useGrokOAuth: () => ({
    authUrl: ref(''), sessionId: ref(''), loading: ref(false), error: ref(''), resetState: vi.fn()
  }) }
})
import ReAuthAccountModal from '../ReAuthAccountModal.vue'

const KiroStub = defineComponent({
  name: 'KiroAuthorizationFlow',
  props: ['account'],
  emits: ['authorized'],
  setup(_, { expose }) {
    const saving = ref(false)
    expose({ saving, reset: mocks.reset })
    return { saving }
  },
  template: '<div><button data-testid="authorized" @click="$emit(\'authorized\', account.id)">authorized</button><button data-testid="saving" @click="saving = true">saving</button></div>'
})
const OAuthStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  emits: ['generate-url'],
  setup(_, { expose }) { expose({ reset: vi.fn(), inputMethod: 'manual' }); return {} },
  template: '<button data-testid="provider-generate" @click="$emit(\'generate-url\')">generate</button>'
})
const account = (id = 42, platform = 'kiro') => ({
  id, platform, type: 'oauth', name: 'Kiro', proxy_id: 8, credentials: { refresh_token: 'old', endpoint: 'cli' },
  group_ids: [10], extra: { mixed_scheduling: true, retained_setting: 'yes' }
}) as Account
const render = (selected = account()) => mount(ReAuthAccountModal, {
  props: { show: true, account: selected },
  global: { stubs: {
    BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
    Icon: true, KiroAuthorizationFlow: KiroStub, OAuthAuthorizationFlow: OAuthStub
  } }
})

describe('active admin Kiro reauthorization modal', () => {
  beforeEach(() => { Object.values(mocks).forEach(mock => mock.mockReset()); mocks.reset.mockResolvedValue(true) })

  it('uses only the selected Kiro branch and emits the returned account', async () => {
    const wrapper = render()
    expect(wrapper.getComponent(KiroStub).props('account').id).toBe(42)
    expect(wrapper.getComponent(KiroStub).props('account')).toMatchObject({
      group_ids: [10], extra: { mixed_scheduling: true, retained_setting: 'yes' }
    })
    expect(wrapper.findComponent(OAuthStub).exists()).toBe(false)
    const updated = { ...account(), credentials: { refresh_token: 'fresh', endpoint: 'cli' } }
    mocks.getById.mockResolvedValue(updated)
    await wrapper.get('[data-testid="authorized"]').trigger('click')
    await flushPromises()
    expect(mocks.getById.mock.calls).toEqual([[42]])
    expect(wrapper.emitted('reauthorized')).toEqual([[updated]])
    expect(mocks.apply).not.toHaveBeenCalled()
    expect(mocks.generate).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('does not emit success after switching away and back during account fetch', async () => {
    const wrapper = render()
    let resolve!: (value: Account) => void
    mocks.getById.mockReturnValue(new Promise<Account>(done => { resolve = done }))
    await wrapper.get('[data-testid="authorized"]').trigger('click')
    await wrapper.setProps({ account: account(43) })
    await wrapper.setProps({ account: account(42) })
    resolve(account())
    await flushPromises()
    expect(wrapper.emitted('reauthorized')).toBeUndefined()
    expect(mocks.success).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('blocks ordinary close during saving and unmounts Kiro when hidden', async () => {
    const wrapper = render()
    await wrapper.get('[data-testid="saving"]').trigger('click')
    await wrapper.get('button.btn-secondary').trigger('click')
    expect(wrapper.emitted('close')).toBeUndefined()
    await wrapper.setProps({ show: false })
    expect(wrapper.findComponent(KiroStub).exists()).toBe(false)
    expect(wrapper.findComponent(OAuthStub).exists()).toBe(false)
    wrapper.unmount()
  })

  it.each(['openai', 'anthropic'])('keeps the existing %s authorization branch', async platform => {
    const wrapper = render(account(1, platform))
    expect(wrapper.findComponent(KiroStub).exists()).toBe(false)
    await wrapper.get('[data-testid="provider-generate"]').trigger('click')
    expect(mocks.generate).toHaveBeenCalledOnce()
    wrapper.unmount()
  })
})
