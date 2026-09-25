import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { defineComponent } from 'vue'
import UsageBillingAnalysis from '../UsageBillingAnalysis.vue'
import type { BillingAnalysis } from '@/api/admin/usage'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

const BaseDialogStub = defineComponent({
  props: { show: Boolean },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})
const analysis: BillingAnalysis = {
  total: { requests: 119, user_cost: 25.71962, account_cost: 160.604579 },
  models: [{ model: 'gpt-6-astra', requests: 119, user_cost: 25.71962, account_cost: 160.604579 }]
}
const mountAnalysis = (data = analysis) => mount(UsageBillingAnalysis, {
  props: {
    analysis: data, loading: false,
    settings: { weekly_cost_usd: 17, weekly_quota_usd: 100 },
    settingsState: 'ready', coefficient: 0.17
  },
  global: { stubs: { BaseDialog: BaseDialogStub, Icon: true } }
})

describe('UsageBillingAnalysis', () => {
  it('shows total and per-model profit from U minus estimated cost without changing U/A', async () => {
    const wrapper = mountAnalysis()
    expect(wrapper.text()).toContain('0.1601x')
    expect(wrapper.text()).toContain('0.17x')
    expect(wrapper.text()).toContain('$27.3028')
    expect(wrapper.text()).toContain('-$1.5832')
    expect(wrapper.findAll('tbody td')[5].classes()).toContain('text-red-600')
    await wrapper.get('tbody button').trigger('click')
    expect(wrapper.emitted('selectModel')?.[0]).toEqual(['gpt-6-astra'])
  })

  it('emits both weekly inputs and recalculates from the parent settings', async () => {
    const wrapper = mountAnalysis()
    await wrapper.get('[data-testid="usage-cost-settings"]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('#weekly-cost-usd').element.value).toBe('17')
    await wrapper.get('#weekly-cost-usd').setValue('20')
    await wrapper.get('#weekly-quota-usd').setValue('80')
    expect(wrapper.text()).toContain('0.25x')
    await wrapper.get('#usage-cost-estimate-form').trigger('submit.prevent')
    const [value, done] = wrapper.emitted('updateSettings')![0] as [{ weekly_cost_usd: number; weekly_quota_usd: number }, (saved: boolean) => void]
    expect(value).toEqual({ weekly_cost_usd: 20, weekly_quota_usd: 80 })
    await wrapper.setProps({ settings: value, coefficient: 0.25 })
    done(true)
    await nextTick()
    expect(wrapper.text()).toContain('$40.1511')
    expect(wrapper.text()).toContain('-$14.4315')
    expect(wrapper.find('#usage-cost-estimate-form').exists()).toBe(false)
  })

  it('leaves profit unavailable when unconfigured and allows clearing the setting', async () => {
    const wrapper = mountAnalysis()
    await wrapper.setProps({ settings: { weekly_cost_usd: 0, weekly_quota_usd: 0 }, coefficient: null })
    expect(wrapper.text()).toContain('usage.notConfigured')
    expect(wrapper.get('tbody').text()).not.toContain('$27.3028')
    await wrapper.get('[data-testid="usage-cost-settings"]').trigger('click')
    await wrapper.get('#weekly-cost-usd').setValue('17')
    expect(wrapper.get('button[form="usage-cost-estimate-form"]').attributes('disabled')).toBeDefined()
    await wrapper.get('#weekly-quota-usd').setValue('100')
    await wrapper.get('#usage-cost-estimate-form').trigger('submit.prevent')
    const done = wrapper.emitted('updateSettings')![0][1] as (saved: boolean) => void
    await wrapper.setProps({ settings: { weekly_cost_usd: 17, weekly_quota_usd: 100 }, coefficient: 0.17 })
    done(true)
    await wrapper.get('[data-testid="usage-cost-settings"]').trigger('click')
    await wrapper.findAll('button').find((button) => button.text() === 'usage.clearCostEstimate')!.trigger('click')
    expect(wrapper.emitted('updateSettings')![1][0]).toEqual({ weekly_cost_usd: 0, weekly_quota_usd: 0 })
  })

  it('keeps the edit dialog open after a save failure', async () => {
    const wrapper = mountAnalysis()
    await wrapper.get('[data-testid="usage-cost-settings"]').trigger('click')
    await wrapper.get('#usage-cost-estimate-form').trigger('submit.prevent')
    const done = wrapper.emitted('updateSettings')![0][1] as (saved: boolean) => void
    done(false)
    expect(wrapper.find('#usage-cost-estimate-form').exists()).toBe(true)
    expect(wrapper.text()).toContain('0.17x')
  })

  it('shows a load failure and emits retry without showing a stale profit', async () => {
    const wrapper = mountAnalysis()
    await wrapper.setProps({ settings: null, settingsState: 'error', coefficient: null })
    expect(wrapper.text()).toContain('usage.costEstimateLoadFailed')
    expect(wrapper.text()).not.toContain('$27.3028')
    await wrapper.get('[data-testid="usage-cost-settings"]').trigger('click')
    await wrapper.findAll('button').find((button) => button.text() === 'usage.retry')!.trigger('click')
    expect(wrapper.emitted('retrySettings')).toHaveLength(1)
  })

  it('shows no U/A ratio for a zero account estimate', () => {
    const wrapper = mountAnalysis({
      total: { requests: 1, user_cost: 1, account_cost: 0 },
      models: [{ model: 'free', requests: 1, user_cost: 1, account_cost: 0 }]
    })
    expect(wrapper.get('tbody').text()).toContain('usage.unavailable')
    expect(wrapper.get('tbody').text()).toContain('$1.0000')
  })
})
