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
    <div class="overflow-x-auto">
      <table class="w-full min-w-[780px] text-left text-sm">
        <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-gray-400">
          <tr>
            <th class="px-4 py-2 font-medium">{{ t('usage.model') }}</th>
            <th class="px-3 py-2 text-right font-medium">{{ t('usage.totalRequests') }}</th>
            <th class="px-3 py-2 text-right font-medium">U</th>
            <th class="px-3 py-2 text-right font-medium">A</th>
            <th class="px-3 py-2 text-right font-medium">{{ t('usage.estimatedCost') }}</th>
            <th class="px-3 py-2 text-right font-medium">{{ t('usage.estimatedProfit') }}</th>
            <th class="px-4 py-2 text-right font-medium">U/A</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-for="row in sortedModels" :key="row.model" class="hover:bg-gray-50 dark:hover:bg-dark-800/60">
            <td class="px-4 py-2">
              <button type="button" class="text-left font-medium text-primary-600 hover:underline dark:text-primary-400" @click="$emit('selectModel', row.model || '')">
                {{ row.model || t('usage.unknown') }}
              </button>
            </td>
            <td class="px-3 py-2 text-right tabular-nums">{{ row.requests.toLocaleString() }}</td>
            <td class="px-3 py-2 text-right tabular-nums">${{ row.user_cost.toFixed(4) }}</td>
            <td class="px-3 py-2 text-right tabular-nums">${{ row.account_cost.toFixed(4) }}</td>
            <td class="px-3 py-2 text-right tabular-nums">{{ coefficient == null ? '-' : `$${(row.account_cost * coefficient).toFixed(4)}` }}</td>
            <td class="px-3 py-2 text-right font-medium tabular-nums" :class="coefficient == null ? '' : profitColor(row.user_cost - row.account_cost * coefficient)">
              {{ coefficient == null ? '-' : formatAmount(row.user_cost - row.account_cost * coefficient) }}
            </td>
            <td class="px-4 py-2 text-right font-medium tabular-nums">{{ ratio(row) }}</td>
          </tr>
          <tr v-if="!sortedModels.length">
            <td colspan="7" class="px-4 py-8 text-center text-gray-500">{{ loading ? t('common.loading') : t('usage.noData') }}</td>
          </tr>
        </tbody>
      </table>
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
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { BillingAnalysis, BillingAnalysisRow, UsageCostEstimate } from '@/api/admin/usage'
import { formatMultiplier } from '@/utils/formatters'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  analysis: BillingAnalysis | null
  loading: boolean
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

const draftCoefficient = computed(() => {
  const cost = Number(weeklyCostInput.value)
  const quota = Number(weeklyQuotaInput.value)
  const value = cost / quota
  return weeklyCostInput.value !== '' && weeklyQuotaInput.value !== '' &&
    cost > 0 && quota > 0 && Number.isFinite(value) ? value : null
})
const sortedModels = computed(() => [...(props.analysis?.models || [])].sort((a, b) => b.account_cost - a.account_cost))
const ratio = (row: BillingAnalysisRow) => row.account_cost > 0
  ? `${(row.user_cost / row.account_cost).toFixed(4)}x`
  : t('usage.unavailable')
const formatAmount = (value: number) => value < 0 ? `-$${Math.abs(value).toFixed(4)}` : `$${value.toFixed(4)}`
const profitColor = (value: number) => value < 0 ? 'text-red-600 dark:text-red-400' : 'text-green-700 dark:text-green-400'

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
