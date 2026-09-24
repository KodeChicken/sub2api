import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import UsageBillingAnalysis from '../UsageBillingAnalysis.vue'
import type { BillingAnalysis } from '@/api/admin/usage'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

const analysis: BillingAnalysis = {
  total: { requests: 119, user_cost: 25.71962, account_cost: 160.604579 },
  models: [{ model: 'gpt-6-astra', requests: 119, user_cost: 25.71962, account_cost: 160.604579 }],
  accounts: [{
    account_id: 42, requests: 119, user_cost: 25.71962, account_cost: 160.604579,
    monthly_cost: 17, seven_day_estimate: 100
  }]
}

describe('UsageBillingAnalysis', () => {
  it('calculates model ratio from sums and opens the model detail', async () => {
    const wrapper = mount(UsageBillingAnalysis, { props: { analysis, loading: false } })
    expect(wrapper.text()).toContain('0.1601x')
    expect(wrapper.text()).toContain('0.039x')
    expect(wrapper.text()).toContain('1/1 (100.0%)')
    await wrapper.get('tbody button').trigger('click')
    expect(wrapper.emitted('selectModel')?.[0]).toEqual(['gpt-6-astra'])
  })

  it('does not show a complete coefficient when an account is unconfigured', () => {
    const incomplete: BillingAnalysis = {
      ...analysis,
      accounts: [
        { ...analysis.accounts[0], account_cost: 80 },
        { account_id: 43, requests: 1, user_cost: 0, account_cost: 80.604579 }
      ]
    }
    const wrapper = mount(UsageBillingAnalysis, { props: { analysis: incomplete, loading: false } })
    expect(wrapper.text()).toContain('1/2')
    expect(wrapper.text()).toContain('usage.unavailable')
    expect(wrapper.text()).not.toContain('0.039x')
  })

  it('shows no ratio for a zero account estimate', () => {
    const wrapper = mount(UsageBillingAnalysis, {
      props: {
        analysis: {
          total: { requests: 1, user_cost: 1, account_cost: 0 },
          models: [{ model: 'free', requests: 1, user_cost: 1, account_cost: 0 }],
          accounts: []
        },
        loading: false
      }
    })
    expect(wrapper.get('tbody').text()).toContain('usage.unavailable')
  })
})
