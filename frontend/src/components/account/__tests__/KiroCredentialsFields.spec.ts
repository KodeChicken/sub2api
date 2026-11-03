import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import KiroCredentialsFields from '../KiroCredentialsFields.vue'
import { buildKiroCredentials, isKiroMixedCandidate, kiroAccountType, parseKiroCredentialFile } from '@/utils/kiroCredentials'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

describe('Kiro credentials', () => {
  it('limits route candidates to opted-in members with finite valid mappings', () => {
    const account = { platform: 'kiro', type: 'oauth', group_ids: [10], status: 'active', schedulable: true,
      extra: { mixed_scheduling: true }, credentials: { model_mapping: { alias: 'claude-sonnet-4.6' } } } as any
    expect(isKiroMixedCandidate(account, 10)).toBe(true)
    expect(isKiroMixedCandidate(account, 11)).toBe(false)
    expect(isKiroMixedCandidate({ ...account, platform: 'antigravity' }, 10)).toBe(false)
    expect(isKiroMixedCandidate({ ...account, extra: {} }, 10)).toBe(false)
    expect(isKiroMixedCandidate({ ...account, schedulable: false }, 10)).toBe(false)
    for (const mapping of [{ '*': 'claude-sonnet-4.6' }, { alias: '*' }, { alias: '' }, { alias: 'bad\nmodel' }, { alias: 'x'.repeat(257) }]) {
      expect(isKiroMixedCandidate({ ...account, credentials: { model_mapping: mapping } }, 10)).toBe(false)
    }
  })
  it('normalizes native exports without mutating them', () => {
    const credentials = parseKiroCredentialFile(JSON.stringify([{
      authMethod: 'BuilderId', refreshToken: 'refresh', clientId: 'client', clientSecret: 'secret', region: 'eu-west-1'
    }]))
    expect(credentials).toMatchObject({
      auth_method: 'idc', refresh_token: 'refresh', client_id: 'client', client_secret: 'secret',
      auth_region: 'eu-west-1', api_region: 'eu-west-1', endpoint: 'ide'
    })
    expect(credentials).not.toHaveProperty('refreshToken')
    expect(kiroAccountType(credentials)).toBe('oauth')
    expect(kiroAccountType(parseKiroCredentialFile('{"kiroApiKey":"ksk_example"}'))).toBe('apikey')
    expect(() => parseKiroCredentialFile('[{},{}]')).toThrow('singleAccount')
    expect(() => parseKiroCredentialFile('{"name":"no credentials"}')).toThrow('invalidCredentials')
  })

  it('omits blank secrets on edit and keeps the model mapping', () => {
    const built = buildKiroCredentials({ auth_method: 'idc', client_id: 'client',
      refresh_token: '', access_token: ' ', client_secret: '', model_mapping: { alias: 'native-model' } })
    expect(built).not.toHaveProperty('refresh_token')
    expect(built).not.toHaveProperty('access_token')
    expect(built).not.toHaveProperty('client_secret')
    expect(built.model_mapping).toEqual({ alias: 'native-model' })
  })

  it('shows IdC fields and retains aliases when the whitelist changes', async () => {
    const wrapper = mount(KiroCredentialsFields, {
      props: { modelValue: { auth_method: 'idc', model_mapping: { alias: 'native-model' } }, accountId: 42, editing: true, accountType: 'oauth' },
      global: { stubs: { Icon: true, ModelWhitelistSelector: true } }
    })
    expect(wrapper.find('input[id$="-client_secret"]').attributes('type')).toBe('password')
    expect(wrapper.find('input[id$="-client_secret"]').attributes('required')).toBeUndefined()
    const models = wrapper.getComponent({ name: 'ModelWhitelistSelector' })
    expect(models.props('accountId')).toBe(42)
    models.vm.$emit('update:modelValue', ['alias', 'second-model'])
    const update = wrapper.emitted('update:modelValue')?.[0]?.[0] as Record<string, unknown>
    expect(update.model_mapping).toEqual({ alias: 'native-model', 'second-model': 'second-model' })
    await wrapper.get('select[id$="-auth"]').setValue('external_idp')
    expect(wrapper.emitted('update:modelValue')?.[1]?.[0]).toMatchObject({ auth_method: 'external_idp', endpoint: 'ide' })
  })
})
