<template>
  <div class="space-y-5">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div class="flex max-w-full gap-2 overflow-x-auto pb-1">
        <button
          v-for="category in categories"
          :key="category.value"
          type="button"
          class="btn btn-sm flex-shrink-0"
          :class="selectedCategory === category.value ? 'btn-primary' : 'btn-secondary'"
          @click="selectedCategory = category.value"
        >
          {{ category.label }}
        </button>
      </div>
    </div>

    <div class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
      <article
        v-for="material in filteredMaterials"
        :key="material.id"
        class="flex min-h-48 flex-col border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800"
      >
        <div class="flex items-start justify-between gap-3">
          <div>
            <span class="text-xs font-medium text-primary-600 dark:text-primary-400">{{ categoryLabel(material.category) }}</span>
            <h2 class="mt-1 text-base font-semibold text-gray-900 dark:text-white">{{ material.title }}</h2>
          </div>
          <div class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-900/20 dark:text-primary-300">
            <Icon name="sparkles" size="sm" />
          </div>
        </div>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ material.description }}</p>
        <p class="mt-4 line-clamp-3 flex-1 text-sm leading-6 text-gray-700 dark:text-gray-300">{{ material.prompt }}</p>
        <button type="button" class="btn btn-primary btn-sm mt-5 self-start" @click="useMaterial(material.prompt)">
          <Icon name="arrowRight" size="sm" class="mr-1.5" />
          {{ t('imageGeneration.materials.use') }}
        </button>
      </article>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { promptMaterials } from './materials'

const router = useRouter()
const { t } = useI18n()
const selectedCategory = ref('all')

const categories = computed(() => [
  { value: 'all', label: t('imageGeneration.materials.categories.all') },
  { value: 'product', label: t('imageGeneration.materials.categories.product') },
  { value: 'food', label: t('imageGeneration.materials.categories.food') },
  { value: 'poster', label: t('imageGeneration.materials.categories.poster') },
  { value: 'social', label: t('imageGeneration.materials.categories.social') },
  { value: 'travel', label: t('imageGeneration.materials.categories.travel') },
  { value: 'illustration', label: t('imageGeneration.materials.categories.illustration') },
])

const filteredMaterials = computed(() => selectedCategory.value === 'all'
  ? promptMaterials
  : promptMaterials.filter((item) => item.category === selectedCategory.value))

function categoryLabel(value: string) {
  return categories.value.find((item) => item.value === value)?.label || value
}

function useMaterial(prompt: string) {
  sessionStorage.setItem('image-generation-draft-prompt', prompt)
  void router.push('/image-generation/create')
}
</script>
