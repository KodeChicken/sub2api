<template>
  <section class="card overflow-hidden">
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-700">
      <h2 class="text-sm font-semibold">{{ t('usage.billingModels') }}</h2>
      <div class="text-xs text-gray-500 dark:text-gray-400">
        <span v-if="analysis?.accounts.length">
          {{ t('usage.costCoverage') }} {{ coveredAccounts }}/{{ analysis.accounts.length }}
          ({{ coveragePercent.toFixed(1) }}%)
          <span class="mx-1">·</span>
          {{ t('usage.costCoefficient') }}
          <strong class="text-gray-800 dark:text-gray-200">{{ coefficient == null ? t('usage.unavailable') : `${coefficient.toFixed(3)}x` }}</strong>
        </span>
        <span v-else>{{ t('usage.accountEstimateHint') }}</span>
      </div>
    </div>
    <p class="px-4 py-2 text-xs text-gray-500 dark:text-gray-400">{{ t('usage.billingEstimateHint') }}</p>
    <div class="overflow-x-auto">
      <table class="w-full min-w-[520px] text-left text-sm">
        <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-gray-400">
          <tr>
            <th class="px-4 py-2 font-medium">{{ t('usage.model') }}</th>
            <th class="px-3 py-2 text-right font-medium">{{ t('usage.totalRequests') }}</th>
            <th class="px-3 py-2 text-right font-medium">U</th>
            <th class="px-3 py-2 text-right font-medium">A</th>
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
            <td class="px-4 py-2 text-right font-medium tabular-nums">{{ ratio(row) }}</td>
          </tr>
          <tr v-if="!sortedModels.length">
            <td colspan="5" class="px-4 py-8 text-center text-gray-500">{{ loading ? t('common.loading') : t('usage.noData') }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { BillingAnalysis, BillingAnalysisRow } from '@/api/admin/usage'

const props = defineProps<{ analysis: BillingAnalysis | null; loading: boolean }>()
defineEmits<{ selectModel: [model: string] }>()
const { t } = useI18n()

const sortedModels = computed(() => [...(props.analysis?.models || [])].sort((a, b) => b.account_cost - a.account_cost))
const ratio = (row: BillingAnalysisRow) => row.account_cost > 0
  ? `${(row.user_cost / row.account_cost).toFixed(4)}x`
  : t('usage.unavailable')

const validAccounts = computed(() => (props.analysis?.accounts || []).filter(
  (row) => row.monthly_cost != null && row.monthly_cost > 0 &&
    row.seven_day_estimate != null && row.seven_day_estimate > 0
))
const coveredAccounts = computed(() => validAccounts.value.length)
const coveragePercent = computed(() => {
  const total = props.analysis?.total.account_cost || 0
  return total > 0 ? validAccounts.value.reduce((sum, row) => sum + row.account_cost, 0) / total * 100 : 0
})
const coefficient = computed(() => {
  const accounts = props.analysis?.accounts || []
  if (!accounts.length || validAccounts.value.length !== accounts.length) return null
  const weight = accounts.reduce((sum, row) => sum + row.account_cost, 0)
  if (weight <= 0) return null
  return validAccounts.value.reduce(
    (sum, row) => sum + row.account_cost * (row.monthly_cost! / (row.seven_day_estimate! * 52 / 12)),
    0
  ) / weight
})
</script>
