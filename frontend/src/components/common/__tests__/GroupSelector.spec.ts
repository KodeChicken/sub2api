import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import GroupSelector from '../GroupSelector.vue'

const authState = { isSimpleMode: false }

vi.mock('@/stores', () => ({ useAuthStore: () => authState }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const groups = [
  { id: 1, name: 'Basic', platform: 'anthropic', status: 'active' },
  { id: 2, name: 'Composite', platform: 'composite', status: 'active' }
] as any

const mountSelector = (modelValue: number[] = []) => mount(GroupSelector, {
  props: { modelValue, groups },
  global: { stubs: { GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' }, Icon: true } }
})

describe('GroupSelector simple-mode binding policy', () => {
  beforeEach(() => { authState.isSimpleMode = false })

  it('hides composite groups in simple mode and preserves basic groups', () => {
    authState.isSimpleMode = true
    const wrapper = mountSelector()
    expect(wrapper.text()).toContain('Basic')
    expect(wrapper.text()).not.toContain('Composite')
  })

  it('keeps composite groups available in advanced mode', () => {
    const wrapper = mountSelector()
    expect(wrapper.text()).toContain('Composite')
  })

  it('cleans hidden historical composite IDs while preserving visible selections', () => {
    authState.isSimpleMode = true
    const wrapper = mountSelector([1, 2])
    expect(wrapper.emitted('update:modelValue')).toEqual([[[1]]])
  })
})

describe('GroupSelector Kiro mixing', () => {
  const allGroups = [...groups,
    { id: 3, name: 'Kiro', platform: 'kiro', status: 'active' },
    { id: 4, name: 'Gemini', platform: 'gemini', status: 'active' },
    { id: 5, name: 'Codex', platform: 'openai', status: 'active' }
  ]
  it('requires opt-in for new Anthropic bindings without widening to Gemini or OpenAI', async () => {
    const wrapper = mountSelector()
    await wrapper.setProps({ groups: allGroups, platform: 'kiro', mixedScheduling: false })
    expect(wrapper.text()).toContain('Kiro')
    expect(wrapper.text()).not.toContain('Basic')
    await wrapper.setProps({ mixedScheduling: true })
    expect(wrapper.text()).toContain('Basic')
    expect(wrapper.text()).not.toContain('Gemini')
    expect(wrapper.text()).not.toContain('Codex')
  })

  it('keeps selected Anthropic bindings visible on opt-out and allows unbinding', async () => {
    const wrapper = mountSelector([1])
    await wrapper.setProps({ groups: allGroups, platform: 'kiro', mixedScheduling: false })
    expect(wrapper.get<HTMLInputElement>('input[value="1"]').element.checked).toBe(true)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await wrapper.get('input[value="1"]').setValue(false)
    expect(wrapper.emitted('update:modelValue')).toEqual([[[]]])
  })

  it('preserves Antigravity Gemini eligibility and native provider isolation', async () => {
    const wrapper = mountSelector()
    await wrapper.setProps({ groups: allGroups, platform: 'antigravity', mixedScheduling: true })
    expect(wrapper.text()).toContain('Basic')
    expect(wrapper.text()).toContain('Gemini')
    expect(wrapper.text()).not.toContain('Kiro')
    await wrapper.setProps({ platform: 'anthropic' })
    expect(wrapper.text()).toContain('Basic')
    expect(wrapper.text()).not.toContain('Kiro')
    expect(wrapper.text()).not.toContain('Gemini')
  })
})
