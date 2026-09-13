import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'

import SubscriptionsView from '../SubscriptionsView.vue'

const {
  listSubscriptions,
  bulkResetQuota,
  getAllGroups,
  listUsers,
  searchUsageUsers,
  showError,
  showSuccess
} = vi.hoisted(() => ({
  listSubscriptions: vi.fn(),
  bulkResetQuota: vi.fn(),
  getAllGroups: vi.fn(),
  listUsers: vi.fn(),
  searchUsageUsers: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    subscriptions: { list: listSubscriptions, bulkResetQuota },
    groups: { getAll: getAllGroups },
    users: { list: listUsers },
    usage: { searchUsers: searchUsageUsers }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: { id?: number }) =>
        key === 'admin.redeem.userPrefix' ? `User #${params?.id}` : key
    })
  }
})

const DataTableStub = {
  props: ['data', 'selectedKeys'],
  emits: ['update:selectedKeys'],
  template: `
    <div>
      <button
        v-if="data.length"
        data-test="select-first-subscription"
        @click="$emit('update:selectedKeys', [data[0].id])"
      >select</button>
      <div v-for="row in data" :key="row.id">
        <slot name="cell-user" :row="row" />
        <slot name="cell-usage" :row="row" />
      </div>
    </div>
  `
}

const RouterLinkStub = defineComponent({
  name: 'RouterLink',
  props: { to: { type: Object, required: true } },
  template: '<a :href="`${to.path}?user_id=${to.query.user_id}`"><slot /></a>'
})

describe('admin subscription users', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    listSubscriptions.mockResolvedValue({
      items: [{
        id: 9,
        user_id: 42,
        group_id: 3,
        status: 'active',
        starts_at: '2026-01-01T00:00:00Z',
        expires_at: null,
        daily_usage_usd: 0,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        daily_window_start: null,
        weekly_window_start: null,
        monthly_window_start: null,
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        user: { email: 'reader@example.com', username: 'Reader' }
      }],
      total: 1,
      pages: 1
    })
    getAllGroups.mockResolvedValue([])
    listUsers.mockResolvedValue({
      items: [{ id: 42, email: 'reader@example.com' }],
      total: 1,
      pages: 1
    })
    searchUsageUsers.mockResolvedValue([
      { id: 14, email: 'deleted@example.com', deleted: true }
    ])
    bulkResetQuota.mockResolvedValue({ updated_count: 1, subscriptions: [] })
  })

  const mountView = () => mount(SubscriptionsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /></div>' },
        DataTable: DataTableStub,
        RouterLink: RouterLinkStub,
        Pagination: true,
        BaseDialog: {
          props: ['show'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>'
        },
        ConfirmDialog: {
          props: ['show'],
          emits: ['confirm', 'cancel'],
          template: '<button v-if="show" data-test="confirm-dialog" @click="$emit(\'confirm\')">confirm</button>'
        },
        EmptyState: true,
        Select: true,
        GroupBadge: true,
        GroupOptionItem: true,
        Icon: true,
        Teleport: true
      }
    }
  })

  it('searches current users when assigning a subscription', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const wrapper = mountView()
    try {
      await flushPromises()
      await wrapper.findAll('button')
        .find((button) => button.text() === 'admin.subscriptions.assignSubscription')!
        .trigger('click')
      const search = wrapper.get('[data-assign-user-search] input')
      await search.trigger('focus')
      await search.setValue('  example.com  ')
      await vi.advanceTimersByTimeAsync(300)
      await flushPromises()

      expect(listUsers).toHaveBeenCalledWith(1, 30, {
        search: 'example.com', sort_by: 'email', sort_order: 'asc'
      })
      expect(searchUsageUsers).not.toHaveBeenCalled()
      const picker = wrapper.get('[data-assign-user-search]')
      expect(picker.text()).toContain('reader@example.com')
      expect(picker.text()).not.toContain('deleted@example.com')
      await picker.get('button').trigger('click')
      expect((search.element as HTMLInputElement).value).toBe('reader@example.com')
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })

  it('keeps deleted users available when filtering subscription history', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const wrapper = mountView()
    try {
      await flushPromises()
      const search = wrapper.get('[data-filter-user-search] input')
      await search.trigger('focus')
      await search.setValue('deleted')
      await vi.advanceTimersByTimeAsync(300)
      await flushPromises()

      expect(searchUsageUsers).toHaveBeenCalledWith('deleted')
      expect(listUsers).not.toHaveBeenCalled()
      const picker = wrapper.get('[data-filter-user-search]')
      expect(picker.text()).toContain('deleted@example.com')
      await picker.get('button').trigger('click')
      expect(listSubscriptions).toHaveBeenLastCalledWith(
        1, expect.any(Number), expect.objectContaining({ user_id: 14 }), expect.any(Object)
      )
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })

  it('renders the user email as a link to that user filtered usage records', async () => {
    const wrapper = mountView()

    await flushPromises()

    const link = wrapper.getComponent(RouterLinkStub)
    expect(link.text()).toBe('reader@example.com')
    expect(link.props('to')).toEqual({ path: '/admin/usage', query: { user_id: 42 } })
  })

  it('shows the used percentage for every configured quota window without capping overage', async () => {
    listSubscriptions.mockResolvedValue({
      items: [{
        id: 9,
        user_id: 42,
        group_id: 3,
        status: 'active',
        starts_at: '2026-01-01T00:00:00Z',
        expires_at: null,
        daily_usage_usd: 2.5,
        weekly_usage_usd: 22.5,
        monthly_usage_usd: 0,
        daily_window_start: null,
        weekly_window_start: null,
        monthly_window_start: null,
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        user: { email: 'reader@example.com', username: 'Reader' },
        group: {
          id: 3,
          name: 'Pro',
          platform: 'openai',
          subscription_type: 'subscription',
          rate_multiplier: 1,
          daily_limit_usd: 10,
          weekly_limit_usd: 20,
          monthly_limit_usd: 100
        }
      }],
      total: 1,
      pages: 1
    })

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="daily-usage-percentage"]').text()).toBe('25.0%')
    expect(wrapper.get('[data-test="weekly-usage-percentage"]').text()).toBe('112.5%')
    expect(wrapper.get('[data-test="monthly-usage-percentage"]').text()).toBe('0.0%')
  })

  it('uses the user ID label for the usage link when username mode has no username', async () => {
    localStorage.setItem('subscription-user-column-mode', 'username')
    listSubscriptions.mockResolvedValue({
      items: [{
        id: 9,
        user_id: 42,
        group_id: 3,
        status: 'active',
        starts_at: '2026-01-01T00:00:00Z',
        expires_at: null,
        daily_usage_usd: 0,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        daily_window_start: null,
        weekly_window_start: null,
        monthly_window_start: null,
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        user: { email: 'reader@example.com' }
      }],
      total: 1,
      pages: 1
    })

    const wrapper = mountView()
    await flushPromises()

    const link = wrapper.getComponent(RouterLinkStub)
    expect(link.text()).toBe('User #42')
    expect(link.props('to')).toEqual({ path: '/admin/usage', query: { user_id: 42 } })
  })

  it('bulk resets all quota windows for selected subscriptions', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="select-first-subscription"]').trigger('click')
    await wrapper.get('[data-test="bulk-reset-quota"]').trigger('click')
    await wrapper.get('[data-test="confirm-dialog"]').trigger('click')
    await flushPromises()

    expect(bulkResetQuota).toHaveBeenCalledWith({
      subscription_ids: [9],
      daily: true,
      weekly: true,
      monthly: true
    })
    expect(showSuccess).toHaveBeenCalledWith(
      'admin.subscriptions.bulkQuotaResetSuccess'
    )
    expect(wrapper.find('[data-test="bulk-reset-quota"]').exists()).toBe(false)
  })
})
