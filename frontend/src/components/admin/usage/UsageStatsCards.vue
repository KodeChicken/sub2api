<template>
  <div class="grid grid-cols-2 gap-4" :class="billing !== undefined ? 'lg:grid-cols-5' : 'lg:grid-cols-4'">
    <div class="card p-4 flex items-center gap-3">
      <div class="rounded-lg bg-blue-100 p-2 dark:bg-blue-900/30 text-blue-600">
        <Icon name="document" size="md" />
      </div>
      <div>
        <p class="text-xs font-medium text-gray-500">{{ t('usage.totalRequests') }}</p>
        <p class="text-xl font-bold">{{ stats?.total_requests?.toLocaleString() || '0' }}</p>
        <p class="text-xs text-gray-400">{{ t('usage.inSelectedRange') }}</p>
      </div>
    </div>
    <div class="card p-4 flex items-center gap-3">
      <div class="rounded-lg bg-amber-100 p-2 dark:bg-amber-900/30 text-amber-600"><svg class="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="m21 7.5-9-5.25L3 7.5m18 0-9 5.25m9-5.25v9l-9 5.25M3 7.5l9 5.25M3 7.5v9l9 5.25m0-9v9" /></svg></div>
      <div>
        <p class="text-xs font-medium text-gray-500">{{ t('usage.totalTokens') }}</p>
        <p class="text-xl font-bold">{{ formatTokens(stats?.total_tokens || 0) }}</p>
        <p class="flex flex-wrap items-center gap-x-1 text-xs text-gray-500">
          <span>{{ t('usage.in') }}: {{ formatTokens(stats?.total_input_tokens || 0) }}</span>
          <span>/</span>
          <span>{{ t('usage.out') }}: {{ formatTokens(stats?.total_output_tokens || 0) }}</span>
          <span>/</span>
          <span class="group relative inline-flex cursor-help items-center gap-0.5" tabindex="0">
            <span>{{ cacheLabel() }}: {{ formatTokens(stats?.total_cache_tokens || 0) }}</span>
            <svg
              class="h-3.5 w-3.5 text-gray-400"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
              />
            </svg>
            <span
              class="pointer-events-none absolute left-1/2 top-full z-30 mt-2 hidden w-56 -translate-x-1/2 rounded-lg border border-gray-200 bg-white p-3 text-left text-xs text-gray-700 shadow-lg group-hover:block group-focus:block dark:border-dark-600 dark:bg-dark-800 dark:text-dark-200"
            >
              <span class="mb-2 block font-medium text-gray-900 dark:text-white">
                {{ cacheDetailLabel() }}
              </span>
              <span class="flex items-center justify-between gap-3">
                <span>{{ t('usage.cacheCreationTokensLabel') }}</span>
                <span class="tabular-nums">
                  {{ formatTokens(stats?.total_cache_creation_tokens || 0) }}
                </span>
              </span>
              <span class="mt-1 flex items-center justify-between gap-3">
                <span>{{ t('usage.cacheReadTokensLabel') }}</span>
                <span class="tabular-nums">
                  {{ formatTokens(stats?.total_cache_read_tokens || 0) }}
                </span>
              </span>
            </span>
          </span>
        </p>
      </div>
    </div>
    <div class="card p-4 flex items-center gap-3">
      <div class="rounded-lg bg-green-100 p-2 dark:bg-green-900/30 text-green-600">
        <Icon name="dollar" size="md" />
      </div>
      <div class="min-w-0 flex-1">
        <p class="text-xs font-medium text-gray-500">{{ t('usage.totalCost') }}</p>
        <p class="text-xl font-bold text-green-600">
          ${{ (stats?.total_actual_cost || 0).toFixed(4) }}
        </p>
        <p class="text-xs text-gray-400">
          <template v-if="showAccountCost && totalAccountCost != null">
            <span class="text-orange-500">{{ t('usage.accountCost') }} ${{ totalAccountCost.toFixed(4) }}</span>
            <span> · </span>
          </template>
          <span>
            {{ t('usage.standardCost') }}
            <span :class="{ 'line-through': strikeStandardCost }">${{ (stats?.total_cost || 0).toFixed(4) }}</span>
          </span>
        </p>
      </div>
    </div>
    <div v-if="billing !== undefined" class="card flex min-w-0 items-start gap-3 p-4">
      <div class="shrink-0 rounded-lg bg-teal-100 p-2 text-teal-700 dark:bg-teal-900/30 dark:text-teal-300">
        <Icon name="chart" size="md" />
      </div>
      <div class="min-w-0 flex-1">
        <p class="text-xs font-medium text-gray-500">{{ t('usage.billingRatio') }}</p>
        <p class="text-xl font-bold tabular-nums">{{ billing && billing.total.account_cost > 0 ? `${(billing.total.user_cost / billing.total.account_cost).toFixed(4)}x` : t('usage.unavailable') }}</p>
        <div class="mt-1 space-y-0.5 text-xs tabular-nums">
          <p class="text-gray-500">
            {{ t('usage.costCoefficient') }}
            <span class="font-semibold text-gray-800 dark:text-gray-200">
              {{ costSettingsState === 'loading' ? t('common.loading') : costSettingsState === 'error' ? t('usage.costEstimateLoadFailed') : costFactor == null ? t('usage.notConfigured') : `${formatMultiplier(costFactor)}x` }}
            </span>
          </p>
          <p>
            {{ t('usage.estimatedProfit') }}
            <span v-if="billing && costFactor != null" class="font-semibold" :class="billing.total.user_cost - billing.total.account_cost * costFactor < 0 ? 'text-red-600 dark:text-red-400' : 'text-green-700 dark:text-green-400'">
              {{ formatAmount(billing.total.user_cost - billing.total.account_cost * costFactor) }}
            </span>
            <span v-else class="text-gray-500">{{ costSettingsState === 'ready' ? t('usage.notConfigured') : t('usage.unavailable') }}</span>
          </p>
          <p class="break-words text-gray-500">
            U ${{ billing?.total.user_cost?.toFixed(4) || '0.0000' }} ·
            A ${{ billing?.total.account_cost?.toFixed(4) || '0.0000' }}
            <template v-if="billing && costFactor != null">
              · {{ t('usage.estimatedCost') }} ${{ (billing.total.account_cost * costFactor).toFixed(4) }}
            </template>
          </p>
        </div>
      </div>
    </div>
    <div class="card p-4 flex items-center gap-3">
      <div class="rounded-lg bg-purple-100 p-2 dark:bg-purple-900/30 text-purple-600">
        <Icon name="clock" size="md" />
      </div>
      <div><p class="text-xs font-medium text-gray-500">{{ t('usage.avgDuration') }}</p><p class="text-xl font-bold">{{ formatDuration(stats?.average_duration_ms || 0) }}</p></div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminUsageStatsResponse } from '@/api/admin/usage'
import type { BillingAnalysis } from '@/api/admin/usage'
import type { UsageStatsResponse } from '@/types'
import Icon from '@/components/icons/Icon.vue'
import { formatMultiplier } from '@/utils/formatters'

const props = withDefaults(defineProps<{
  stats: (AdminUsageStatsResponse | UsageStatsResponse) | null
  billing?: BillingAnalysis | null
  costFactor?: number | null
  costSettingsState?: 'loading' | 'error' | 'ready'
  showAccountCost?: boolean
  strikeStandardCost?: boolean
}>(), {
  showAccountCost: true,
  strikeStandardCost: false,
  costFactor: null,
  costSettingsState: 'ready',
})

const { t } = useI18n()

const totalAccountCost = computed(() => {
  const stats = props.stats as (AdminUsageStatsResponse & { total_account_cost?: number }) | null
  return stats?.total_account_cost ?? null
})
const showAccountCost = computed(() => props.showAccountCost)
const strikeStandardCost = computed(() => props.strikeStandardCost)

const formatDuration = (ms: number) =>
  ms < 1000 ? `${ms.toFixed(0)}ms` : `${(ms / 1000).toFixed(2)}s`
const formatAmount = (value: number) => value < 0 ? `-$${Math.abs(value).toFixed(4)}` : `$${value.toFixed(4)}`

const formatTokens = (value: number) => {
  if (value >= 1e9) return (value / 1e9).toFixed(2) + 'B'
  if (value >= 1e6) return (value / 1e6).toFixed(2) + 'M'
  if (value >= 1e3) return (value / 1e3).toFixed(2) + 'K'
  return value.toLocaleString()
}

const cacheLabel = () => t('usage.cacheTotal')
const cacheDetailLabel = () => t('usage.cacheBreakdown')
</script>
