<template>
  <AppLayout>
    <section class="flex min-h-[calc(100vh-8rem)] flex-col gap-5">
      <header class="flex flex-col gap-4 border-b border-gray-200 pb-4 dark:border-dark-700 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('imageGeneration.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('imageGeneration.description') }}</p>
        </div>
        <nav class="flex max-w-full gap-1 overflow-x-auto" :aria-label="t('imageGeneration.title')">
          <RouterLink
            v-for="tab in tabs"
            :key="tab.to"
            :to="tab.to"
            class="inline-flex min-h-10 flex-shrink-0 items-center gap-2 rounded-lg px-4 text-sm font-medium transition-colors"
            :class="isActive(tab.to)
              ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/25 dark:text-primary-300'
              : 'text-gray-600 hover:bg-gray-100 hover:text-gray-900 dark:text-gray-300 dark:hover:bg-dark-800 dark:hover:text-white'"
          >
            <Icon :name="tab.icon" size="sm" />
            {{ tab.label }}
          </RouterLink>
        </nav>
      </header>
      <RouterView class="min-h-0 flex-1" />
    </section>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'

const route = useRoute()
const { t } = useI18n()

const tabs = computed(() => [
  { to: '/image-generation/create', label: t('imageGeneration.tabs.create'), icon: 'sparkles' as const },
  { to: '/image-generation/templates', label: t('imageGeneration.tabs.templates'), icon: 'lightbulb' as const },
  { to: '/image-generation/history', label: t('imageGeneration.tabs.history'), icon: 'clock' as const },
])

function isActive(path: string) {
  return route.path === path
}
</script>
