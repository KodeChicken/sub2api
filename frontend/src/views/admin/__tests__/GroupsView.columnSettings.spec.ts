import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import type { AdminGroup } from '@/types'
import GroupsView from '../GroupsView.vue'

const {
  listGroups,
  getAllGroups,
  getModelAllowlistCandidates,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
  getTemporaryDispatchQuotaPreview,
  getTemporaryDispatch,
  adjustTemporaryDispatch,
  startTemporaryDispatch,
  stopTemporaryDispatch,
  listAccounts,
  showError,
  showSuccess,
  isCurrentStep,
  nextStep,
  authState,
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  getAllGroups: vi.fn(),
  getModelAllowlistCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  getTemporaryDispatchQuotaPreview: vi.fn(),
  getTemporaryDispatch: vi.fn(),
  adjustTemporaryDispatch: vi.fn(),
  startTemporaryDispatch: vi.fn(),
  stopTemporaryDispatch: vi.fn(),
  listAccounts: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  isCurrentStep: vi.fn(),
  nextStep: vi.fn(),
  authState: { isSimpleMode: false },
}))

const messages: Record<string, string> = {
  'admin.groups.columnSettings': 'Column Settings',
  'admin.groups.columns.name': 'Name',
  'admin.groups.columns.id': 'ID',
  'admin.groups.columns.platform': 'Platform',
  'admin.groups.columns.billingType': 'Billing Type',
  'admin.groups.columns.rateMultiplier': 'Rate Multiplier',
  'admin.groups.columns.type': 'Type',
  'admin.groups.columns.accounts': 'Accounts',
  'admin.groups.columns.temporaryDispatch': 'Temporary dispatch',
  'admin.groups.columns.capacity': 'Capacity',
  'admin.groups.columns.usage': 'Usage',
  'admin.groups.columns.status': 'Status',
  'admin.groups.columns.actions': 'Actions',
  'admin.groups.temporaryDispatch.action': 'Temporary account',
  'admin.groups.temporaryDispatch.adjustAction': 'Adjust temporary dispatch',
  'admin.groups.temporaryDispatch.adjustSubmit': 'Apply adjustment',
  'admin.groups.temporaryDispatch.adjusted': 'Temporary dispatch adjusted',
  'admin.groups.temporaryDispatch.start': 'Start takeover',
  'admin.groups.temporaryDispatch.started': 'Temporary dispatch started',
  'admin.groups.temporaryDispatch.modeHybrid': 'Quota or time',
  'admin.groups.usageToday': 'Today',
  'admin.groups.usageYesterday': 'Yesterday',
  'admin.groups.usageTotal': 'Total',
}

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      getAll: getAllGroups,
      getModelAllowlistCandidates,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      getTemporaryDispatchQuotaPreview,
      getTemporaryDispatch,
      adjustTemporaryDispatch,
      startTemporaryDispatch,
      stopTemporaryDispatch,
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      updateSortOrder: vi.fn(),
    },
    accounts: {
      list: listAccounts,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState,
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    isCurrentStep,
    nextStep,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

const createGroup = (overrides: Partial<AdminGroup> = {}): AdminGroup => ({
  id: 1,
  name: 'Core Anthropic',
  description: null,
  platform: 'anthropic',
  rate_multiplier: 1,
  rpm_limit: 0,
  is_exclusive: false,
  status: 'active',
  subscription_type: 'standard',
  daily_limit_usd: null,
  weekly_limit_usd: null,
  monthly_limit_usd: null,
  allow_image_generation: false,
  image_rate_independent: false,
  image_rate_multiplier: 1,
  image_price_1k: null,
  image_price_2k: null,
  image_price_4k: null,
  claude_code_only: false,
  fallback_group_id: null,
  fallback_group_id_on_invalid_request: null,
  allow_messages_dispatch: false,
  default_mapped_model: '',
  messages_dispatch_model_config: undefined,
  require_oauth_only: false,
  require_privacy_set: false,
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-01T00:00:00Z',
  model_routing: null,
  model_routing_enabled: false,
  mcp_xml_inject: true,
  supported_model_scopes: [],
  account_count: 3,
  active_account_count: 2,
  rate_limited_account_count: 1,
  model_allowlist: undefined,
  sort_order: 10,
  ...overrides,
})

const AppLayoutStub = {
  template: '<div><slot /></div>',
}

const TablePageLayoutStub = {
  template: `
    <div>
      <slot name="filters" />
      <slot name="table" />
      <slot name="pagination" />
    </div>
  `,
}

const DataTableStub = {
  props: ['columns', 'data', 'selectedKeys'],
  emits: ['sort', 'update:selectedKeys'],
  template: `
    <div>
      <div data-test="columns">{{ columns.map((col) => col.key).join(',') }}</div>
      <div data-test="rows">{{ data.map((row) => row.name).join(',') }}</div>
      <button v-if="data.length" data-test="select-first" @click="$emit('update:selectedKeys', [data[0].id])">select</button>
      <div v-if="data.length" data-test="usage-cell">
        <slot name="cell-usage" :row="data[0]" />
      </div>
    </div>
  `,
}

const SelectStub = {
  props: ['modelValue', 'options', 'placeholder'],
  emits: ['update:modelValue', 'change'],
  template: `
    <select
      :value="modelValue"
      @change="$emit('update:modelValue', $event.target.value); $emit('change')"
    >
      <option v-for="option in options" :key="String(option.value)" :value="option.value">
        {{ option.label }}
      </option>
    </select>
  `,
}

const BaseDialogStub = {
  props: ['show'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

const IconStub = {
  props: ['name'],
  template: '<span data-test="icon">{{ name }}</span>',
}

const mountView = async () => {
  const wrapper = mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: SelectStub,
        PlatformIcon: true,
        Icon: IconStub,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        VueDraggable: { template: '<div><slot /></div>' },
      },
    },
  })
  await flushPromises()
  return wrapper
}

const columnKeys = (wrapper: ReturnType<typeof mount>) =>
  wrapper.get('[data-test="columns"]').text().split(',').filter(Boolean)

const openColumnSettings = async (wrapper: ReturnType<typeof mount>) => {
  await wrapper.get('button[title="Column Settings"]').trigger('click')
}

const clickColumnToggle = async (wrapper: ReturnType<typeof mount>, label: string) => {
  const button = wrapper
    .findAll('button')
    .find((item) => item.text().includes(label))
  expect(button, `column toggle ${label}`).toBeTruthy()
  await button!.trigger('click')
  await flushPromises()
}

describe('admin GroupsView column settings', () => {
  beforeEach(() => {
    localStorage.clear()

    listGroups.mockReset()
    getAllGroups.mockReset()
    getModelAllowlistCandidates.mockReset()
    getUsageSummary.mockReset()
    getCapacitySummary.mockReset()
    getLiveCapability.mockReset()
    getTemporaryDispatchQuotaPreview.mockReset()
    getTemporaryDispatch.mockReset()
    adjustTemporaryDispatch.mockReset()
    startTemporaryDispatch.mockReset()
    stopTemporaryDispatch.mockReset()
    listAccounts.mockReset()
    showError.mockReset()
    showSuccess.mockReset()
    isCurrentStep.mockReset()
    nextStep.mockReset()
    authState.isSimpleMode = false

    listGroups.mockResolvedValue({
      items: [createGroup()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getAllGroups.mockResolvedValue([])
    getModelAllowlistCandidates.mockResolvedValue([])
    getUsageSummary.mockResolvedValue([])
    getCapacitySummary.mockResolvedValue([])
    getLiveCapability.mockResolvedValue({ supported: false })
    getTemporaryDispatchQuotaPreview.mockResolvedValue({
      window: '5h',
      used_percent: 35,
      reset_at: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
    })
    startTemporaryDispatch.mockResolvedValue({
      dispatch_id: 'td_test', group_ids: [1], account_id: 42,
      started_at: '2026-07-01T00:00:00Z', expires_at: '2026-07-01T02:00:00Z',
    })
    stopTemporaryDispatch.mockResolvedValue({ message: 'ok' })
    listAccounts.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    isCurrentStep.mockReturnValue(false)
  })

  it('does not call advanced group APIs or expose the exclusive filter in simple mode', async () => {
    authState.isSimpleMode = true
    const wrapper = await mountView()

    expect(getLiveCapability).not.toHaveBeenCalled()
    expect(getModelAllowlistCandidates).not.toHaveBeenCalled()
    expect(getUsageSummary).not.toHaveBeenCalled()
    expect(getCapacitySummary).not.toHaveBeenCalled()
    expect(listGroups).toHaveBeenCalledWith(
      expect.any(Number),
      expect.any(Number),
      expect.objectContaining({ is_exclusive: undefined }),
      expect.anything(),
    )
    expect(wrapper.find('select').text()).not.toContain('admin.groups.allGroups')
  })

  afterEach(() => {
    localStorage.clear()
  })

  it('hides the id column by default while keeping other group columns visible', async () => {
    const wrapper = await mountView()

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'platform',
      'billing_type',
      'rate_multiplier',
      'is_exclusive',
      'account_count',
      'temporary_dispatch',
      'capacity',
      'usage',
      'status',
      'actions',
    ])
    expect(localStorage.getItem('group-hidden-columns')).toBe(JSON.stringify(['id']))
    expect(localStorage.getItem('group-column-settings-version')).toBe('2')
  })

  it('starts a temporary dispatch for selected groups', async () => {
    listAccounts.mockResolvedValue({
      items: [{ id: 42, name: 'Drain account', type: 'apikey' }],
      total: 1,
      page: 1,
      page_size: 30,
      pages: 1,
    })
    const wrapper = await mountView()

    await wrapper.get('[data-test="select-first"]').trigger('click')
    const openButton = wrapper.findAll('button').find((item) => item.text().includes('Temporary account'))
    expect(openButton).toBeTruthy()
    await openButton!.trigger('click')
    await flushPromises()

    const accountButton = wrapper.findAll('button').find((item) => item.text().includes('Drain account'))
    expect(accountButton).toBeTruthy()
    await accountButton!.trigger('click')
    const startButton = wrapper.findAll('button').find((item) => item.text().includes('Start takeover'))
    expect(startButton).toBeTruthy()
    await startButton!.trigger('click')
    await flushPromises()

    expect(startTemporaryDispatch).toHaveBeenCalledWith({
      group_ids: [1],
      accounts: [{
        account_id: 42,
        duration_minutes: 120,
        quota_window: undefined,
        target_delta_percent: undefined,
      }],
      mode: 'time',
    })
    expect(showSuccess).toHaveBeenCalledWith('Temporary dispatch started')
  })

  it('defaults OpenAI OAuth dispatch to hybrid mode with the 5h window', async () => {
    listGroups.mockResolvedValue({
      items: [createGroup({ name: 'Core OpenAI', platform: 'openai' })],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    listAccounts.mockResolvedValue({
      items: [{ id: 42, name: 'Drain OAuth', type: 'oauth', parent_account_id: null }],
      total: 1,
      page: 1,
      page_size: 30,
      pages: 1,
    })
    const wrapper = await mountView()

    await wrapper.get('[data-test="select-first"]').trigger('click')
    const openButton = wrapper.findAll('button').find((item) => item.text().includes('Temporary account'))
    await openButton!.trigger('click')
    await flushPromises()

    const accountButton = wrapper.findAll('button').find((item) => item.text().includes('Drain OAuth'))
    await accountButton!.trigger('click')
    await flushPromises()

    expect(getTemporaryDispatchQuotaPreview).toHaveBeenCalledWith(42, '5h')
    const startButton = wrapper.findAll('button').find((item) => item.text().includes('Start takeover'))
    await startButton!.trigger('click')
    await flushPromises()

    expect(startTemporaryDispatch).toHaveBeenCalledWith({
      group_ids: [1],
      accounts: [{
        account_id: 42,
        duration_minutes: 120,
        quota_window: '5h',
        target_delta_percent: 30,
      }],
      mode: 'hybrid',
    })
  })

  it('keeps an OpenAI API key upstream selectable and submits an account-cost target', async () => {
    listGroups.mockResolvedValue({
      items: [createGroup({ name: 'Core OpenAI', platform: 'openai' })],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    listAccounts.mockResolvedValue({
      items: [{ id: 42, name: '0.04x upstream', type: 'apikey', rate_multiplier: 0.04 }],
      total: 1,
      page: 1,
      page_size: 30,
      pages: 1,
    })
    const wrapper = await mountView()

    await wrapper.get('[data-test="select-first"]').trigger('click')
    await wrapper.findAll('button').find((item) => item.text().includes('Temporary account'))!.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find((item) => item.text().includes('0.04x upstream'))!.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find((item) => item.text().includes('Start takeover'))!.trigger('click')
    await flushPromises()

    expect(startTemporaryDispatch).toHaveBeenCalledWith({
      group_ids: [1],
      accounts: [{
        account_id: 42,
        duration_minutes: 120,
        quota_window: '5h',
        target_delta_percent: undefined,
        target_cost: 200,
      }],
      mode: 'hybrid',
    })
    expect(getTemporaryDispatchQuotaPreview).not.toHaveBeenCalled()
  })

  it('appends usage to an existing shared temporary dispatch', async () => {
    const future = new Date(Date.now() + 60 * 60 * 1000).toISOString()
    listGroups.mockResolvedValue({
      items: [createGroup({
        temporary_dispatch_id: 'td_shared',
        temporary_dispatch_account_id: 42,
        temporary_dispatch_account_ids: [42],
        temporary_dispatch_expires_at: future,
        temporary_dispatch_mode: 'hybrid',
      })],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getTemporaryDispatch.mockResolvedValue({
      dispatch_id: 'td_shared',
      group_ids: [1, 2],
      account_id: 42,
      mode: 'hybrid',
      started_at: new Date().toISOString(),
      expires_at: future,
      accounts: [{
        account_id: 42,
        account_name: 'Drain OAuth',
        usage_metric: 'quota_percent',
        current_percent: 55,
        target_percent: 65,
        expires_at: future,
      }],
    })
    adjustTemporaryDispatch.mockResolvedValue({ dispatch_id: 'td_shared' })
    const wrapper = await mountView()

    await wrapper.get('[data-test="select-first"]').trigger('click')
    await wrapper.findAll('button').find((item) => item.text().includes('Adjust temporary dispatch'))!.trigger('click')
    await flushPromises()
    const inputs = wrapper.findAll('input[type="number"]')
    await inputs[0].setValue(10)
    await wrapper.findAll('button').find((item) => item.text().includes('Apply adjustment'))!.trigger('click')
    await flushPromises()

    expect(adjustTemporaryDispatch).toHaveBeenCalledWith({
      group_id: 1,
      accounts: [{ account_id: 42, additional_usage: 10, extend_duration_minutes: 0 }],
    })
    expect(showSuccess).toHaveBeenCalledWith('Temporary dispatch adjusted')
  })

  it('selects multiple temporary accounts with independent durations', async () => {
    listAccounts.mockResolvedValue({
      items: [
        { id: 42, name: 'Drain one', type: 'apikey' },
        { id: 43, name: 'Drain two', type: 'apikey' },
      ],
      total: 2,
      page: 1,
      page_size: 30,
      pages: 1,
    })
    const wrapper = await mountView()

    await wrapper.get('[data-test="select-first"]').trigger('click')
    await wrapper.findAll('button').find((item) => item.text().includes('Temporary account'))!.trigger('click')
    await flushPromises()

    await wrapper.findAll('button').find((item) => item.text().includes('Drain one'))!.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find((item) => item.text().includes('Drain two') && item.text().includes('#43'))!.trigger('click')
    await flushPromises()

    const durations = wrapper.findAll('input[type="number"]')
    expect(durations).toHaveLength(2)
    await durations[0].setValue(15)
    await durations[1].setValue(90)

    await wrapper.findAll('button').find((item) => item.text().includes('Start takeover'))!.trigger('click')
    await flushPromises()

    expect(startTemporaryDispatch).toHaveBeenCalledWith({
      group_ids: [1],
      accounts: [
        { account_id: 42, duration_minutes: 15, quota_window: undefined, target_delta_percent: undefined },
        { account_id: 43, duration_minutes: 90, quota_window: undefined, target_delta_percent: undefined },
      ],
      mode: 'time',
    })
  })

  it('removes a selected temporary account from its chip button', async () => {
    listAccounts.mockResolvedValue({
      items: [
        { id: 42, name: 'Drain one', type: 'apikey' },
        { id: 43, name: 'Drain two', type: 'apikey' },
      ],
      total: 2,
      page: 1,
      page_size: 30,
      pages: 1,
    })
    const wrapper = await mountView()

    await wrapper.get('[data-test="select-first"]').trigger('click')
    await wrapper.findAll('button').find((item) => item.text().includes('Temporary account'))!.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find((item) => item.text().includes('Drain one'))!.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find((item) => item.text().includes('Drain two') && item.text().includes('#43'))!.trigger('click')
    await flushPromises()

    await wrapper.get('button[aria-label="admin.groups.temporaryDispatch.removeAccount"]').trigger('click')
    await wrapper.findAll('button').find((item) => item.text().includes('Start takeover'))!.trigger('click')
    await flushPromises()

    expect(startTemporaryDispatch).toHaveBeenCalledWith(expect.objectContaining({
      accounts: [expect.objectContaining({ account_id: 43 })],
    }))
  })

  it('applies saved hidden columns on mount and ignores unknown keys', async () => {
    localStorage.setItem(
      'group-hidden-columns',
      JSON.stringify(['usage', 'capacity', 'removed_column', 'name', 'actions']),
    )
    localStorage.setItem('group-column-settings-version', '2')

    const wrapper = await mountView()

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'id',
      'platform',
      'billing_type',
      'rate_multiplier',
      'is_exclusive',
      'account_count',
      'temporary_dispatch',
      'status',
      'actions',
    ])
  })

  it('auto-hides id for existing saved column prefs after version bump', async () => {
    localStorage.setItem('group-hidden-columns', JSON.stringify(['usage']))
    // No version key → treated as version 1, migrate to 2 and hide id.

    const wrapper = await mountView()

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'platform',
      'billing_type',
      'rate_multiplier',
      'is_exclusive',
      'account_count',
      'temporary_dispatch',
      'capacity',
      'status',
      'actions',
    ])
    expect(JSON.parse(localStorage.getItem('group-hidden-columns')!)).toEqual(
      expect.arrayContaining(['usage', 'id']),
    )
    expect(localStorage.getItem('group-column-settings-version')).toBe('2')
  })

  it('toggles a column and persists hidden column keys', async () => {
    const wrapper = await mountView()

    await openColumnSettings(wrapper)
    await clickColumnToggle(wrapper, 'Usage')

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'platform',
      'billing_type',
      'rate_multiplier',
      'is_exclusive',
      'account_count',
      'temporary_dispatch',
      'capacity',
      'status',
      'actions',
    ])
    expect(JSON.parse(localStorage.getItem('group-hidden-columns')!)).toEqual(
      expect.arrayContaining(['id', 'usage']),
    )
  })

  it('can show the id column from column settings', async () => {
    const wrapper = await mountView()

    await openColumnSettings(wrapper)
    await clickColumnToggle(wrapper, 'ID')

    expect(columnKeys(wrapper)).toEqual([
      'name',
      'id',
      'platform',
      'billing_type',
      'rate_multiplier',
      'is_exclusive',
      'account_count',
      'temporary_dispatch',
      'capacity',
      'usage',
      'status',
      'actions',
    ])
    expect(localStorage.getItem('group-hidden-columns')).toBe(JSON.stringify([]))
  })

  it('skips usage and capacity fetches until consuming columns are shown', async () => {
    localStorage.setItem(
      'group-hidden-columns',
      JSON.stringify(['billing_type', 'usage', 'capacity']),
    )

    const wrapper = await mountView()

    expect(getUsageSummary).not.toHaveBeenCalled()
    expect(getCapacitySummary).not.toHaveBeenCalled()

    await openColumnSettings(wrapper)
    await clickColumnToggle(wrapper, 'Usage')
    expect(getUsageSummary).toHaveBeenCalledTimes(1)
    expect(getUsageSummary).toHaveBeenCalledWith()
    expect(getCapacitySummary).not.toHaveBeenCalled()

    await clickColumnToggle(wrapper, 'Capacity')
    expect(getUsageSummary).toHaveBeenCalledTimes(1)
    expect(getCapacitySummary).toHaveBeenCalledTimes(1)
  })

  it('renders yesterday usage between today and total', async () => {
    getUsageSummary.mockResolvedValue([
      { group_id: 1, today_cost: 1.25, yesterday_cost: 2.5, total_cost: 9.75 },
    ])

    const wrapper = await mountView()
    const text = wrapper.get('[data-test="usage-cell"]').text()

    expect(text).toContain('Today$1.25')
    expect(text).toContain('Yesterday$2.50')
    expect(text).toContain('Total$9.75')
    expect(text.indexOf('Today')).toBeLessThan(text.indexOf('Yesterday'))
    expect(text.indexOf('Yesterday')).toBeLessThan(text.indexOf('Total'))
  })
})
