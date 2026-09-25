import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { defineComponent } from 'vue'
import UsageBillingAnalysis from '../UsageBillingAnalysis.vue'
import type { BillingAnalysis } from '@/api/admin/usage'

const { getUsers } = vi.hoisted(() => ({ getUsers: vi.fn() }))
vi.mock('@/api/admin/usage', () => ({
  adminUsageAPI: { getBillingAnalysisUsers: getUsers }
}))
vi.mock('vue-chartjs', () => ({
  Doughnut: { props: ['data'], template: '<div data-testid="billing-doughnut">{{ data.datasets[0].data.join(",") }}</div>' }
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
  props: {
    analysis: data, loading: false,
    filters: { start_date: '2026-09-25', end_date: '2026-09-25', group_id: 7 },
    settings: { weekly_cost_usd: 17, weekly_quota_usd: 100 },
    settingsState: 'ready', coefficient: 0.17
  },
  global: { stubs: { BaseDialog: BaseDialogStub, Icon: true, Doughnut: true, LoadingSpinner: true } }
})

describe('UsageBillingAnalysis', () => {
  beforeEach(() => {
    getUsers.mockReset().mockResolvedValue({ users: [], has_more: false })
  })

  it('shows total and per-model profit from U minus estimated cost without changing U/A', async () => {
    const wrapper = mountAnalysis()
    expect(wrapper.text()).toContain('0.1601x')
    expect(wrapper.text()).toContain('0.17x')
    expect(wrapper.text()).toContain('$27.3028')
    expect(wrapper.text()).toContain('-$1.5832')
    expect(wrapper.findAll('tbody td')[5].classes()).toContain('text-red-600')
    await wrapper.get('[aria-label="usage.viewModelRecords"]').trigger('click')
    expect(wrapper.emitted('selectModel')?.[0]).toEqual(['gpt-6-astra'])
  })

  it('expands by username, loads pages lazily and uses the same U/A and profit formula', async () => {
    getUsers.mockResolvedValueOnce({
      users: [{ user_id: 42, username: 'Alice', requests: 2, user_cost: 5, account_cost: 20 }],
      has_more: true
    }).mockResolvedValueOnce({
      users: [{ user_id: 43, username: 'Bob', requests: 1, user_cost: 1, account_cost: 10 }],
      has_more: false
    })
    const wrapper = mountAnalysis()
    expect(getUsers).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="billing-expand-gpt-6-astra"]').trigger('click')
    await flushPromises()
    expect(getUsers).toHaveBeenCalledWith(expect.objectContaining({
      model: 'gpt-6-astra', group_id: 7, start_date: '2026-09-25', page: 1, page_size: 50
    }))
    expect(wrapper.text()).toContain('Alice')
    expect(wrapper.text()).toContain('$3.4000')
    expect(wrapper.text()).toContain('$1.6000')
    expect(wrapper.text()).toContain('0.2500x')
    expect(wrapper.text()).not.toContain('alice@example.com')
    await wrapper.findAll('button').find((button) => button.text() === 'usage.loadMoreUsers')!.trigger('click')
    await flushPromises()
    expect(getUsers).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 }))
    expect(wrapper.text()).toContain('Bob')
    await wrapper.get('[data-testid="billing-expand-gpt-6-astra"]').trigger('click')
    expect(wrapper.text()).not.toContain('Bob')
  })

  it('switches the doughnut between requests and U without charting negative profit', async () => {
    const wrapper = mountAnalysis({
      total: { requests: 3, user_cost: 7, account_cost: 30 },
      models: [
        { model: 'first', requests: 2, user_cost: 5, account_cost: 20 },
        { model: 'second', requests: 1, user_cost: 2, account_cost: 10 }
      ]
    })
    expect(wrapper.get('[data-testid="billing-doughnut"]').text()).toBe('2,1')
    await wrapper.findAll('button').find((button) => button.text() === 'U')!.trigger('click')
    expect(wrapper.get('[data-testid="billing-doughnut"]').text()).toBe('5,2')
  })

  it('does not display stale user details after the active filters change', async () => {
    let resolveUsers!: (value: { users: any[]; has_more: boolean }) => void
    getUsers.mockReturnValueOnce(new Promise((resolve) => { resolveUsers = resolve }))
    const wrapper = mountAnalysis()
    await wrapper.get('[data-testid="billing-expand-gpt-6-astra"]').trigger('click')
    await wrapper.setProps({ filters: { group_id: 8 } })
    resolveUsers({ users: [{ user_id: 2, username: 'stale', requests: 1, user_cost: 1, account_cost: 1 }], has_more: false })
    await flushPromises()
    expect(wrapper.text()).not.toContain('stale')
    expect(wrapper.find('[aria-expanded="true"]').exists()).toBe(false)
  })

  it('supports the unknown model and retries a failed detail request', async () => {
    getUsers.mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce({
      users: [{ user_id: 2, username: '', requests: 1, user_cost: 1, account_cost: 1 }],
      has_more: false
    })
    const wrapper = mountAnalysis({
      total: { requests: 1, user_cost: 1, account_cost: 1 },
      models: [{ model: '', requests: 1, user_cost: 1, account_cost: 1 }]
    })
    await wrapper.get('[data-testid="billing-expand-"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('usage.usersLoadFailed')
    await wrapper.findAll('button').find((button) => button.text() === 'usage.retry')!.trigger('click')
    await flushPromises()
    expect(getUsers).toHaveBeenLastCalledWith(expect.objectContaining({ model: '', page: 1 }))
    expect(wrapper.text()).toContain('usage.userIdFallback')
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
