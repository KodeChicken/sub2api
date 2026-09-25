<template>
  <section class="card overflow-hidden">
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-700">
      <h2 class="text-sm font-semibold">{{ t('usage.billingModels') }}</h2>
      <div class="flex flex-wrap items-center gap-3 text-xs text-gray-500 dark:text-gray-400">
        <span>
          {{ t('usage.costCoefficient') }}
          <strong class="text-gray-800 dark:text-gray-200">
            {{ settingsState === 'loading' ? t('common.loading') : settingsState === 'error' ? t('usage.costEstimateLoadFailed') : coefficient == null ? t('usage.notConfigured') : `${formatMultiplier(coefficient)}x` }}
          </strong>
        </span>
        <span v-if="analysis && coefficient != null" class="tabular-nums">
          {{ t('usage.estimatedCost') }} <strong class="text-gray-800 dark:text-gray-200">${{ (analysis.total.account_cost * coefficient).toFixed(4) }}</strong>
        </span>
        <span v-if="analysis && coefficient != null" class="tabular-nums">
          {{ t('usage.estimatedProfit') }}
          <strong :class="profitColor(analysis.total.user_cost - analysis.total.account_cost * coefficient)">
            {{ formatAmount(analysis.total.user_cost - analysis.total.account_cost * coefficient) }}
          </strong>
        </span>
        <button
          type="button"
          class="rounded p-1 text-gray-500 hover:bg-gray-100 hover:text-gray-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:hover:bg-dark-700 dark:hover:text-white"
          :title="t('usage.configureCostEstimate')"
          :aria-label="t('usage.configureCostEstimate')"
          data-testid="usage-cost-settings"
          @click="openSettings"
        >
          <Icon name="cog" size="sm" />
        </button>
      </div>
    </div>
    <p class="px-4 py-2 text-xs text-gray-500 dark:text-gray-400">{{ t('usage.billingEstimateHint') }}</p>
    <div v-if="loading" class="flex h-48 items-center justify-center"><LoadingSpinner /></div>
    <div v-else-if="!sortedModels.length" class="flex h-48 items-center justify-center text-sm text-gray-500">{{ t('usage.noData') }}</div>
    <div v-else class="flex flex-col items-center gap-4 p-4 lg:flex-row lg:items-start lg:gap-6">
      <div class="flex w-full shrink-0 flex-col items-center gap-3 lg:w-48">
        <div class="inline-flex rounded-lg border border-gray-200 bg-gray-50 p-0.5 dark:border-dark-700 dark:bg-dark-800">
          <button type="button" class="rounded-md px-2.5 py-1 text-xs font-medium" :class="chartMetric === 'requests' ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500'" @click="chartMetric = 'requests'">
            {{ t('usage.totalRequests') }}
          </button>
          <button type="button" class="rounded-md px-2.5 py-1 text-xs font-medium" :class="chartMetric === 'user_cost' ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500'" @click="chartMetric = 'user_cost'">
            U
          </button>
        </div>
        <div class="h-48 w-48">
          <Doughnut v-if="chartData" :data="chartData" :options="chartOptions" />
          <div v-else class="flex h-full items-center justify-center text-xs text-gray-500">{{ t('usage.noData') }}</div>
        </div>
      </div>
      <div class="max-h-[520px] w-full min-w-0 flex-1 overflow-auto">
        <table class="w-full min-w-[780px] table-fixed text-left text-xs">
          <colgroup>
            <col class="w-[23%]" /><col class="w-[11%]" /><col class="w-[12%]" />
            <col class="w-[12%]" /><col class="w-[14%]" /><col class="w-[15%]" /><col class="w-[13%]" />
          </colgroup>
          <thead class="text-gray-500 dark:text-gray-400">
            <tr>
              <th class="pb-2 text-left font-medium">{{ t('usage.model') }}</th>
              <th class="pb-2 text-right font-medium">{{ t('usage.totalRequests') }}</th>
              <th class="pb-2 text-right font-medium">U</th>
              <th class="pb-2 text-right font-medium">A</th>
              <th class="pb-2 text-right font-medium">{{ t('usage.estimatedCost') }}</th>
              <th class="pb-2 text-right font-medium">{{ t('usage.estimatedProfit') }}</th>
              <th class="pb-2 text-right font-medium">U/A</th>
            </tr>
          </thead>
          <tbody>
            <template v-for="row in sortedModels" :key="modelKey(row)">
              <tr class="border-t border-gray-100 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-700/40">
                <td class="py-1.5">
                  <div class="flex items-center gap-1">
                    <button
                      type="button"
                      class="flex min-w-0 items-center gap-1 text-left font-medium text-blue-600 dark:text-blue-400"
                      :aria-expanded="expandedModel === modelKey(row)"
                      :aria-label="t('usage.expandModelUsers', { model: row.model || t('usage.unknown') })"
                      :data-testid="`billing-expand-${modelKey(row)}`"
                      @click="toggleModel(modelKey(row))"
                    >
                      <Icon :name="expandedModel === modelKey(row) ? 'chevronDown' : 'chevronRight'" size="xs" />
                      <span class="max-w-[180px] truncate" :title="row.model || t('usage.unknown')">{{ row.model || t('usage.unknown') }}</span>
                    </button>
                    <button
                      v-if="row.model"
                      type="button"
                      class="shrink-0 rounded p-1 text-gray-400 hover:text-primary-600"
                      :title="t('usage.viewModelRecords')"
                      :aria-label="t('usage.viewModelRecords')"
                      @click="emit('selectModel', row.model)"
                    >
                      <Icon name="search" size="xs" />
                    </button>
                  </div>
                </td>
                <td class="py-1.5 text-right tabular-nums">{{ row.requests.toLocaleString() }}</td>
                <td class="py-1.5 text-right tabular-nums text-green-600 dark:text-green-400">${{ row.user_cost.toFixed(4) }}</td>
                <td class="py-1.5 text-right tabular-nums text-orange-500">${{ row.account_cost.toFixed(4) }}</td>
                <td class="py-1.5 text-right tabular-nums">{{ coefficient == null ? '-' : `$${(row.account_cost * coefficient).toFixed(4)}` }}</td>
                <td class="py-1.5 text-right font-medium tabular-nums" :class="coefficient == null ? '' : profitColor(row.user_cost - row.account_cost * coefficient)">
                  {{ coefficient == null ? '-' : formatAmount(row.user_cost - row.account_cost * coefficient) }}
                </td>
                <td class="py-1.5 text-right font-medium tabular-nums">{{ ratio(row) }}</td>
              </tr>
              <tr v-if="expandedModel === modelKey(row)" class="bg-gray-50/60 dark:bg-dark-700/30">
                <td colspan="7" class="p-0">
                  <div v-if="breakdownLoading && !users.length" class="flex justify-center py-4"><LoadingSpinner /></div>
                  <div v-else-if="breakdownError && !users.length" class="py-3 text-center text-gray-500">
                    {{ t('usage.usersLoadFailed') }}
                    <button type="button" class="ml-2 text-primary-600 hover:underline" @click="loadUsers(1)">{{ t('usage.retry') }}</button>
                  </div>
                  <div v-else-if="!users.length" class="py-3 text-center text-gray-500">{{ t('usage.noData') }}</div>
                  <div v-else>
                    <table class="w-full table-fixed text-xs">
                      <colgroup>
                        <col class="w-[23%]" /><col class="w-[11%]" /><col class="w-[12%]" />
                        <col class="w-[12%]" /><col class="w-[14%]" /><col class="w-[15%]" /><col class="w-[13%]" />
                      </colgroup>
                      <tbody>
                        <tr v-for="user in users" :key="user.user_id" class="border-t border-gray-100 dark:border-dark-700">
                          <td class="max-w-[180px] truncate py-1.5 pl-5 font-medium" :title="user.username || `#${user.user_id}`">
                            {{ user.username || t('usage.userIdFallback', { id: user.user_id }) }}
                          </td>
                          <td class="py-1.5 text-right tabular-nums">{{ user.requests.toLocaleString() }}</td>
                          <td class="py-1.5 text-right tabular-nums text-green-600 dark:text-green-400">${{ user.user_cost.toFixed(4) }}</td>
                          <td class="py-1.5 text-right tabular-nums text-orange-500">${{ user.account_cost.toFixed(4) }}</td>
                          <td class="py-1.5 text-right tabular-nums">{{ coefficient == null ? '-' : `$${(user.account_cost * coefficient).toFixed(4)}` }}</td>
                          <td class="py-1.5 text-right font-medium tabular-nums" :class="coefficient == null ? '' : profitColor(user.user_cost - user.account_cost * coefficient)">
                            {{ coefficient == null ? '-' : formatAmount(user.user_cost - user.account_cost * coefficient) }}
                          </td>
                          <td class="py-1.5 text-right font-medium tabular-nums">{{ ratio(user) }}</td>
                        </tr>
                      </tbody>
                    </table>
                    <div v-if="hasMore || breakdownLoading || breakdownError" class="py-2 text-center">
                      <button v-if="breakdownError" type="button" class="text-primary-600 hover:underline" @click="loadUsers(nextPage)">{{ t('usage.retry') }}</button>
                      <button v-else-if="hasMore" type="button" class="text-primary-600 hover:underline disabled:opacity-50" :disabled="breakdownLoading" @click="loadUsers(nextPage)">
                        {{ breakdownLoading ? t('common.loading') : t('usage.loadMoreUsers') }}
                      </button>
                    </div>
                  </div>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
    </div>
  </section>

  <BaseDialog :show="showSettings" :title="t('usage.configureCostEstimate')" width="narrow" @close="showSettings = false">
    <div v-if="settingsState === 'loading'" class="py-8 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
    <div v-else-if="settingsState === 'error'" class="py-6 text-center">
      <p class="text-sm text-red-600">{{ t('usage.costEstimateLoadFailed') }}</p>
      <button type="button" class="btn btn-secondary mt-4" @click="$emit('retrySettings')">{{ t('usage.retry') }}</button>
    </div>
    <form v-else id="usage-cost-estimate-form" class="space-y-5" @submit.prevent="saveSettings">
      <div>
        <label for="weekly-cost-usd" class="input-label">{{ t('usage.weeklyCostUSD') }}</label>
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-500">$</span>
          <input id="weekly-cost-usd" v-model="weeklyCostInput" type="number" min="0" step="any" class="input pl-7" required />
        </div>
      </div>
      <div>
        <label for="weekly-quota-usd" class="input-label">{{ t('usage.weeklyQuotaUSD') }}</label>
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-500">$</span>
          <input id="weekly-quota-usd" v-model="weeklyQuotaInput" type="number" min="0" step="any" class="input pl-7" required />
        </div>
      </div>
      <p class="text-sm tabular-nums text-gray-700 dark:text-gray-200">
        {{ t('usage.costCoefficient') }}:
        {{ draftCoefficient == null ? t('usage.unavailable') : `${formatMultiplier(draftCoefficient)}x` }}
      </p>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('usage.globalCostEstimateHint') }}</p>
    </form>
    <template #footer>
      <button v-if="coefficient != null && settingsState === 'ready'" type="button" class="btn btn-secondary mr-auto" :disabled="settingsSaving" @click="clearSettings">
        {{ t('usage.clearCostEstimate') }}
      </button>
      <button type="button" class="btn btn-secondary" @click="showSettings = false">{{ t('common.cancel') }}</button>
      <button v-if="settingsState === 'ready'" type="submit" form="usage-cost-estimate-form" class="btn btn-primary" :disabled="draftCoefficient == null || settingsSaving">
        {{ settingsSaving ? t('common.saving') : t('common.save') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, ArcElement, Tooltip, Legend } from 'chart.js'
import type { ChartOptions, TooltipItem } from 'chart.js'
import { Doughnut } from 'vue-chartjs'
import { adminUsageAPI } from '@/api/admin/usage'
import type { AdminUsageQueryParams, BillingAnalysis, BillingAnalysisRow, BillingAnalysisUserRow, UsageCostEstimate } from '@/api/admin/usage'
import { formatMultiplier } from '@/utils/formatters'
import BaseDialog from '@/components/common/BaseDialog.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'

ChartJS.register(ArcElement, Tooltip, Legend)

const props = defineProps<{
  analysis: BillingAnalysis | null
  loading: boolean
  filters: AdminUsageQueryParams
  settings: UsageCostEstimate | null
  settingsState: 'loading' | 'error' | 'ready'
  coefficient: number | null
}>()
const emit = defineEmits<{
  selectModel: [model: string]
  retrySettings: []
  updateSettings: [value: UsageCostEstimate, done: (saved: boolean) => void]
}>()
const { t } = useI18n()

const settingsSaving = ref(false)
const showSettings = ref(false)
const weeklyCostInput = ref('')
const weeklyQuotaInput = ref('')
const chartMetric = ref<'requests' | 'user_cost'>('requests')
const expandedModel = ref<string | null>(null)
const users = ref<BillingAnalysisUserRow[]>([])
const breakdownLoading = ref(false)
const breakdownError = ref(false)
const hasMore = ref(false)
const nextPage = ref(1)
let breakdownSeq = 0

const draftCoefficient = computed(() => {
  const cost = Number(weeklyCostInput.value)
  const quota = Number(weeklyQuotaInput.value)
  const value = cost / quota
  return weeklyCostInput.value !== '' && weeklyQuotaInput.value !== '' &&
    cost > 0 && quota > 0 && Number.isFinite(value) ? value : null
})
const sortedModels = computed(() => [...(props.analysis?.models || [])].sort((a, b) => b.account_cost - a.account_cost))
const modelKey = (row: BillingAnalysisRow) => row.model || ''
const chartData = computed(() => {
  const sorted = [...sortedModels.value].sort((a, b) => b[chartMetric.value] - a[chartMetric.value])
  const top = sorted.slice(0, 8)
  const remaining = sorted.slice(8).reduce((sum, row) => sum + row[chartMetric.value], 0)
  const data = top.map((row) => row[chartMetric.value])
  if (remaining > 0) data.push(remaining)
  if (!data.some((value) => value > 0)) return null
  return {
    labels: [...top.map((row) => row.model || t('usage.unknown')), ...(remaining > 0 ? [t('usage.otherModels')] : [])],
    datasets: [{ data, backgroundColor: ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899', '#14b8a6', '#f97316', '#94a3b8'], borderWidth: 0 }]
  }
})
const chartOptions: ChartOptions<'doughnut'> = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: { display: false },
    tooltip: {
      callbacks: {
        label: (context: TooltipItem<'doughnut'>) => {
          const value = Number(context.raw)
          const total = context.dataset.data.reduce((sum, value) => sum + value, 0)
          const amount = chartMetric.value === 'requests'
            ? value.toLocaleString()
            : `$${value.toFixed(4)}`
          return `${context.label}: ${amount} (${total > 0 ? (value / total * 100).toFixed(1) : '0.0'}%)`
        }
      }
    }
  }
}
const ratio = (row: BillingAnalysisRow | BillingAnalysisUserRow) => row.account_cost > 0
  ? `${(row.user_cost / row.account_cost).toFixed(4)}x`
  : t('usage.unavailable')
const formatAmount = (value: number) => value < 0 ? `-$${Math.abs(value).toFixed(4)}` : `$${value.toFixed(4)}`
const profitColor = (value: number) => value < 0 ? 'text-red-600 dark:text-red-400' : 'text-green-700 dark:text-green-400'

const loadUsers = async (page: number) => {
  if (expandedModel.value === null || breakdownLoading.value) return
  const seq = ++breakdownSeq
  breakdownLoading.value = true
  breakdownError.value = false
  try {
    const result = await adminUsageAPI.getBillingAnalysisUsers({
      ...props.filters, model: expandedModel.value, page, page_size: 50
    })
    if (seq !== breakdownSeq) return
    users.value = page === 1 ? result.users : [...users.value, ...result.users]
    hasMore.value = result.has_more
    nextPage.value = page + 1
  } catch {
    if (seq === breakdownSeq) breakdownError.value = true
  } finally {
    if (seq === breakdownSeq) breakdownLoading.value = false
  }
}
const toggleModel = (model: string) => {
  ++breakdownSeq
  if (expandedModel.value === model) {
    expandedModel.value = null
    return
  }
  expandedModel.value = model
  users.value = []
  hasMore.value = false
  breakdownLoading.value = false
  void loadUsers(1)
}
watch(() => [props.analysis, props.filters], () => {
  ++breakdownSeq
  expandedModel.value = null
  users.value = []
  breakdownLoading.value = false
})

const openSettings = () => {
  showSettings.value = true
  weeklyCostInput.value = props.settings?.weekly_cost_usd ? String(props.settings.weekly_cost_usd) : ''
  weeklyQuotaInput.value = props.settings?.weekly_quota_usd ? String(props.settings.weekly_quota_usd) : ''
}
const updateSettings = async (value: UsageCostEstimate) => {
  if (settingsSaving.value) return
  settingsSaving.value = true
  emit('updateSettings', value, (saved) => {
    settingsSaving.value = false
    if (saved) showSettings.value = false
  })
}
const saveSettings = () => {
  if (draftCoefficient.value == null || settingsSaving.value) return
  void updateSettings({
    weekly_cost_usd: Number(weeklyCostInput.value),
    weekly_quota_usd: Number(weeklyQuotaInput.value)
  })
}
const clearSettings = () => {
  if (settingsSaving.value) return
  void updateSettings({ weekly_cost_usd: 0, weekly_quota_usd: 0 })
}

</script>
