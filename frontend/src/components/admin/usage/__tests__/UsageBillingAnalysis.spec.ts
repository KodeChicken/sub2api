import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { defineComponent } from 'vue'
import UsageBillingAnalysis from '../UsageBillingAnalysis.vue'
import type { BillingAnalysis, BillingAnalysisRow } from '@/api/admin/usage'

const { getUsers } = vi.hoisted(() => ({ getUsers: vi.fn() }))
vi.mock('@/api/admin/usage', () => ({
  adminUsageAPI: { getBillingAnalysisUsers: getUsers }
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string, params?: { priced: number; total: number }) =>
    params ? `${key}:${params.priced}/${params.total}` : key })
}))

const BaseDialogStub = defineComponent({
  props: { show: Boolean },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})
const billingRow = (row: Omit<BillingAnalysisRow,
  'non_image_requests' | 'non_image_priced_requests' | 'non_image_user_cost' | 'non_image_official_reference_cost'
>): BillingAnalysisRow => ({
  ...row,
  non_image_requests: row.requests,
  non_image_priced_requests: row.priced_requests,
  non_image_user_cost: row.user_cost,
  non_image_official_reference_cost: row.official_reference_cost
})
const analysis: BillingAnalysis = {
  total: billingRow({ requests: 119, priced_requests: 119, user_cost: 25.71962, account_cost: 160.604579, official_reference_cost: 80.3022895 }),
  models: [billingRow({ model: 'gpt-6-astra', requests: 119, priced_requests: 119, user_cost: 25.71962, account_cost: 160.604579, official_reference_cost: 80.3022895 })]
}
const mountAnalysis = (data = analysis) => mount(UsageBillingAnalysis, {
  props: {
    analysis: data, loading: false,
    filters: { start_date: '2026-09-25', end_date: '2026-09-25', group_id: 7 },
    settings: { weekly_cost_usd: 17, weekly_quota_usd: 100 },
    settingsState: 'ready', coefficient: 0.17
  },
  global: { stubs: { BaseDialog: BaseDialogStub, Icon: true, LoadingSpinner: true } }
})

describe('UsageBillingAnalysis', () => {
  beforeEach(() => {
    getUsers.mockReset().mockResolvedValue({ users: [], has_more: false })
  })

  it('shows profit from U minus estimated cost and the real U/catalog ratio', async () => {
    const wrapper = mountAnalysis()
    expect(wrapper.text()).toContain('0.3203x')
    expect(wrapper.text()).toContain('0.17x')
    expect(wrapper.text()).toContain('$27.3028')
    expect(wrapper.text()).toContain('-$1.5832')
    expect(wrapper.findAll('tbody td')[4].classes()).toContain('text-red-600')
    await wrapper.get('[aria-label="usage.viewModelRecords"]').trigger('click')
    expect(wrapper.emitted('selectModel')?.[0]).toEqual(['gpt-6-astra'])
  })

  it('matches the model distribution email labels, loads pages lazily and keeps the same billing formulas', async () => {
    getUsers.mockResolvedValueOnce({
      users: [{ user_id: 42, username: 'Alice', email: 'alice@example.com', ...billingRow({ requests: 2, priced_requests: 2, user_cost: 5, account_cost: 20, official_reference_cost: 10 }) }],
      has_more: true
    }).mockResolvedValueOnce({
      users: [{ user_id: 43, username: '', email: '1173379996@qq.com', ...billingRow({ requests: 1, priced_requests: 1, user_cost: 1, account_cost: 10, official_reference_cost: 4 }) }],
      has_more: false
    })
    const wrapper = mountAnalysis()
    expect(getUsers).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="billing-expand-gpt-6-astra"]').trigger('click')
    await flushPromises()
    expect(getUsers).toHaveBeenCalledWith(expect.objectContaining({
      model: 'gpt-6-astra', group_id: 7, start_date: '2026-09-25', page: 1, page_size: 50
    }))
    expect(wrapper.text()).toContain('alice@example.com')
    expect(wrapper.text()).not.toContain('Alice')
    const userLabel = wrapper.get('td[title="alice@example.com"]')
    expect(userLabel.classes()).toEqual(expect.arrayContaining(['pl-6', 'text-gray-600', 'dark:text-gray-300']))
    expect(userLabel.classes()).not.toContain('font-medium')
    expect(userLabel.get('span').classes()).toContain('truncate')
    expect(wrapper.text()).toContain('$3.4000')
    expect(wrapper.text()).toContain('$1.6000')
    expect(wrapper.text()).toContain('0.5000x')
    await wrapper.findAll('button').find((button) => button.text() === 'usage.loadMoreUsers')!.trigger('click')
    await flushPromises()
    expect(getUsers).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 }))
    expect(wrapper.text()).toContain('1173379996@qq.com')
    expect(wrapper.text()).not.toContain('User #43')
    await wrapper.get('[data-testid="billing-expand-gpt-6-astra"]').trigger('click')
    expect(wrapper.text()).not.toContain('1173379996@qq.com')
  })

  it('shows only the requested billing columns and each model real ratio', () => {
    const wrapper = mountAnalysis({
      total: billingRow({ requests: 3, priced_requests: 3, user_cost: 7, account_cost: 30, official_reference_cost: 20 }),
      models: [
        billingRow({ model: 'first', requests: 2, priced_requests: 2, user_cost: 5, account_cost: 20, official_reference_cost: 10 }),
        billingRow({ model: 'second', requests: 1, priced_requests: 1, user_cost: 2, account_cost: 10, official_reference_cost: 10 })
      ]
    })
    expect(wrapper.findAll('thead th').map((cell) => cell.text())).toEqual([
      'usage.model', 'U', 'A', 'usage.estimatedCost', 'usage.estimatedProfit', 'usage.realRatio'
    ])
    expect(wrapper.get('tbody').text()).toContain('0.5000x')
    expect(wrapper.get('tbody').text()).toContain('0.2000x')
  })

  it('does not display stale user details after the active filters change', async () => {
    let resolveUsers!: (value: { users: any[]; has_more: boolean }) => void
    getUsers.mockReturnValueOnce(new Promise((resolve) => { resolveUsers = resolve }))
    const wrapper = mountAnalysis()
    await wrapper.get('[data-testid="billing-expand-gpt-6-astra"]').trigger('click')
    await wrapper.setProps({ filters: { group_id: 8 } })
    resolveUsers({ users: [{ user_id: 2, username: 'stale', email: 'stale@example.com', ...billingRow({ requests: 1, priced_requests: 1, user_cost: 1, account_cost: 1, official_reference_cost: 1 }) }], has_more: false })
    await flushPromises()
    expect(wrapper.text()).not.toContain('stale')
    expect(wrapper.find('[aria-expanded="true"]').exists()).toBe(false)
  })

  it('supports the unknown model and retries a failed detail request', async () => {
    getUsers.mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce({
      users: [{ user_id: 2, username: 'Alice', email: '', ...billingRow({ requests: 1, priced_requests: 1, user_cost: 1, account_cost: 1, official_reference_cost: 1 }) }],
      has_more: false
    })
    const wrapper = mountAnalysis({
      total: billingRow({ requests: 1, priced_requests: 1, user_cost: 1, account_cost: 1, official_reference_cost: 1 }),
      models: [billingRow({ model: '', requests: 1, priced_requests: 1, user_cost: 1, account_cost: 1, official_reference_cost: 1 })]
    })
    await wrapper.get('[data-testid="billing-expand-"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('usage.usersLoadFailed')
    await wrapper.findAll('button').find((button) => button.text() === 'usage.retry')!.trigger('click')
    await flushPromises()
    expect(getUsers).toHaveBeenLastCalledWith(expect.objectContaining({ model: '', page: 1 }))
    expect(wrapper.text()).toContain('User #2')
    expect(wrapper.text()).not.toContain('Alice')
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

  it('shows no real ratio when no official reference cost is available', () => {
    const wrapper = mountAnalysis({
      total: billingRow({ requests: 1, priced_requests: 0, user_cost: 1, account_cost: 0, official_reference_cost: 0 }),
      models: [billingRow({ model: 'free', requests: 1, priced_requests: 0, user_cost: 1, account_cost: 0, official_reference_cost: 0 })]
    })
    expect(wrapper.get('tbody').text()).toContain('usage.unavailable')
    expect(wrapper.get('tbody').text()).toContain('$1.0000')
  })

  it('does not mix all user charges with a partially covered reference cost', () => {
    const wrapper = mountAnalysis({
      total: billingRow({ requests: 2, priced_requests: 1, user_cost: 2, account_cost: 4, official_reference_cost: 1 }),
      models: [billingRow({ model: 'partial', requests: 2, priced_requests: 1, user_cost: 2, account_cost: 4, official_reference_cost: 1 })]
    })
    expect(wrapper.get('tbody').text()).toContain('usage.unavailable')
    expect(wrapper.get('tbody').text()).not.toContain('2.0000x')
  })

  it('excludes image charges from model and user real ratios without changing U, A or profit', async () => {
    const row = {
      ...billingRow({ model: 'mixed', requests: 3, priced_requests: 2, user_cost: 5.05, account_cost: 20, official_reference_cost: 10 }),
      non_image_requests: 2,
      non_image_priced_requests: 2,
      non_image_user_cost: 5
    }
    getUsers.mockResolvedValueOnce({
      users: [{ user_id: 42, username: '', email: 'mixed@example.com', ...row }],
      has_more: false
    })
    const wrapper = mountAnalysis({ total: row, models: [row] })
    const cells = wrapper.findAll('tbody tr')[0].findAll('td')
    expect(cells.map((cell) => cell.text()).slice(1)).toEqual([
      '$5.0500', '$20.0000', '$3.4000', '$1.6500', '0.5000x'
    ])
    expect(cells[5].attributes('title')).toBe('usage.realRatioCoverage:2/2')
    await wrapper.get('[data-testid="billing-expand-mixed"]').trigger('click')
    await flushPromises()
    const userCells = wrapper.get('td[title="mixed@example.com"]').element.parentElement!.querySelectorAll('td')
    expect(Array.from(userCells).slice(1).map((cell) => cell.textContent?.trim())).toEqual([
      '$5.0500', '$20.0000', '$3.4000', '$1.6500', '0.5000x'
    ])
    expect(userCells[5].getAttribute('title')).toBe('usage.realRatioCoverage:2/2')
  })

  it('shows no real ratio for image-only rows even when they have catalog reference costs', () => {
    const row = {
      ...billingRow({ model: 'images', requests: 1, priced_requests: 1, user_cost: 0.05, account_cost: 0.1, official_reference_cost: 0.1 }),
      non_image_requests: 0,
      non_image_priced_requests: 0,
      non_image_user_cost: 0,
      non_image_official_reference_cost: 0
    }
    const wrapper = mountAnalysis({ total: row, models: [row] })
    const cells = wrapper.findAll('tbody td')
    expect(cells[1].text()).toBe('$0.0500')
    expect(cells[5].text()).toBe('usage.unavailable')
    expect(cells[5].attributes('title')).toBe('usage.realRatioCoverage:0/0')
  })

  it('still hides the real ratio when a non-image request has no catalog pricing', () => {
    const row = {
      ...billingRow({ model: 'mixed', requests: 3, priced_requests: 1, user_cost: 5.05, account_cost: 20, official_reference_cost: 10 }),
      non_image_requests: 2,
      non_image_priced_requests: 1,
      non_image_user_cost: 5
    }
    const wrapper = mountAnalysis({ total: row, models: [row] })
    const cell = wrapper.findAll('tbody td')[5]
    expect(cell.text()).toBe('usage.unavailable')
    expect(cell.attributes('title')).toBe('usage.realRatioCoverage:1/2')
  })
})
