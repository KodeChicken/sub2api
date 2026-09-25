import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import UsageBillingAnalysis from '../UsageBillingAnalysis.vue'
import type { BillingAnalysis } from '@/api/admin/usage'

const { getSettings, updateSettings, showError, showSuccess } = vi.hoisted(() => ({
  getSettings: vi.fn(),
  updateSettings: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin/usage', () => ({
  adminUsageAPI: { getUsageCostEstimate: getSettings, updateUsageCostEstimate: updateSettings }
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess })
}))
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
  props: { analysis: data, loading: false },
  global: { stubs: { BaseDialog: BaseDialogStub, Icon: true } }
})

describe('UsageBillingAnalysis', () => {
  beforeEach(() => {
    getSettings.mockReset().mockResolvedValue({ weekly_cost_usd: 17, weekly_quota_usd: 100 })
    updateSettings.mockReset().mockImplementation(async (value) => value)
    showError.mockReset()
    showSuccess.mockReset()
  })

  it('uses one global factor for total and per-model estimates without changing U/A', async () => {
    const wrapper = mountAnalysis()
    await flushPromises()
    expect(wrapper.text()).toContain('0.1601x')
    expect(wrapper.text()).toContain('0.17x')
    expect(wrapper.text()).toContain('$27.3028')
    await wrapper.get('tbody button').trigger('click')
    expect(wrapper.emitted('selectModel')?.[0]).toEqual(['gpt-6-astra'])
    wrapper.unmount()
  })

  it('saves both weekly inputs and immediately recalculates the estimate', async () => {
    const wrapper = mountAnalysis()
    await flushPromises()
    await wrapper.get('[data-testid="usage-cost-settings"]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('#weekly-cost-usd').element.value).toBe('17')
    await wrapper.get('#weekly-cost-usd').setValue('20')
    await wrapper.get('#weekly-quota-usd').setValue('80')
    expect(wrapper.text()).toContain('0.25x')
    await wrapper.get('#usage-cost-estimate-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith({ weekly_cost_usd: 20, weekly_quota_usd: 80 })
    expect(wrapper.text()).toContain('$40.1511')
    expect(showSuccess).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows unconfigured state, validates both inputs, and allows clearing', async () => {
    getSettings.mockResolvedValue({ weekly_cost_usd: 0, weekly_quota_usd: 0 })
    const wrapper = mountAnalysis()
    await flushPromises()
    expect(wrapper.text()).toContain('usage.notConfigured')
    expect(wrapper.get('tbody').text()).not.toContain('$27.3028')
    await wrapper.get('[data-testid="usage-cost-settings"]').trigger('click')
    await wrapper.get('#weekly-cost-usd').setValue('17')
    expect(wrapper.get('button[form="usage-cost-estimate-form"]').attributes('disabled')).toBeDefined()
    await wrapper.get('#weekly-quota-usd').setValue('100')
    await wrapper.get('#usage-cost-estimate-form').trigger('submit.prevent')
    await flushPromises()
    expect(wrapper.text()).toContain('0.17x')
    await wrapper.get('[data-testid="usage-cost-settings"]').trigger('click')
    await wrapper.findAll('button').find((button) => button.text() === 'usage.clearCostEstimate')!.trigger('click')
    await flushPromises()
    expect(updateSettings).toHaveBeenLastCalledWith({ weekly_cost_usd: 0, weekly_quota_usd: 0 })
    expect(wrapper.text()).toContain('usage.notConfigured')
    wrapper.unmount()
  })

  it('keeps the previous factor when saving fails', async () => {
    updateSettings.mockRejectedValueOnce(new Error('network'))
    const wrapper = mountAnalysis()
    await flushPromises()
    await wrapper.get('[data-testid="usage-cost-settings"]').trigger('click')
    await wrapper.get('#weekly-cost-usd').setValue('20')
    await wrapper.get('#usage-cost-estimate-form').trigger('submit.prevent')
    await flushPromises()
    expect(wrapper.text()).toContain('0.17x')
    expect(showError).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows a load failure and retries from the settings control', async () => {
    getSettings.mockRejectedValueOnce(new Error('network'))
    const wrapper = mountAnalysis()
    await flushPromises()
    expect(wrapper.text()).toContain('usage.costEstimateLoadFailed')
    expect(wrapper.text()).not.toContain('$27.3028')
    await wrapper.get('[data-testid="usage-cost-settings"]').trigger('click')
    await flushPromises()
    expect(getSettings).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('0.17x')
    wrapper.unmount()
  })

  it('shows no U/A ratio for a zero account estimate', async () => {
    const wrapper = mountAnalysis({
      total: { requests: 1, user_cost: 1, account_cost: 0 },
      models: [{ model: 'free', requests: 1, user_cost: 1, account_cost: 0 }]
    })
    await flushPromises()
    expect(wrapper.get('tbody').text()).toContain('usage.unavailable')
    wrapper.unmount()
  })
})
