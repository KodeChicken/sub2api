<template>
  <div class="space-y-5">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <SearchInput v-model="search" :placeholder="t('imageGeneration.history.search')" class="w-full sm:w-72" />
      <button type="button" class="btn btn-secondary" :disabled="loading" @click="loadHistory">
        <Icon name="refresh" size="sm" class="mr-2" :class="loading ? 'animate-spin' : ''" />
        {{ t('common.refresh') }}
      </button>
    </div>

    <div v-if="loading" class="grid min-h-64 place-items-center">
      <LoadingSpinner />
    </div>

    <EmptyState
      v-else-if="filteredRecords.length === 0"
      :title="t('imageGeneration.history.emptyTitle')"
      :description="t('imageGeneration.history.emptyDescription')"
      :action-text="t('imageGeneration.history.start')"
      action-to="/image-generation/create"
    />

    <div v-else class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
      <article
        v-for="record in filteredRecords"
        :key="record.id"
        class="overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800"
      >
        <button type="button" class="block aspect-square w-full bg-gray-100 dark:bg-dark-900" @click="previewRecord = record">
          <img
            v-if="record.images[0]"
            :src="displayURL(record, 0)"
            :alt="record.prompt"
            class="h-full w-full object-contain"
          />
        </button>
        <div class="space-y-3 p-4">
          <p class="line-clamp-2 text-sm font-medium text-gray-900 dark:text-white">{{ record.prompt }}</p>
          <div class="flex flex-wrap gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span>{{ record.model }}</span>
            <span>{{ record.size }}</span>
            <span>{{ formatDate(record.createdAt) }}</span>
          </div>
          <div class="flex items-center justify-between gap-2">
            <button type="button" class="btn btn-secondary btn-sm" @click="reuse(record)">
              <Icon name="refresh" size="xs" class="mr-1.5" />
              {{ t('imageGeneration.history.reuse') }}
            </button>
            <button type="button" class="rounded-lg p-2 text-gray-400 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-950/30" :title="t('common.delete')" @click="remove(record.id)">
              <Icon name="trash" size="sm" />
            </button>
          </div>
        </div>
      </article>
    </div>

    <div v-if="previewRecord" class="fixed inset-0 z-[100000000] grid place-items-center bg-black/70 p-4" @click.self="previewRecord = null">
      <section class="max-h-[92vh] w-full max-w-5xl overflow-hidden rounded-lg bg-white shadow-2xl dark:bg-dark-900">
        <header class="flex items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-dark-700">
          <p class="truncate pr-4 text-sm font-medium text-gray-900 dark:text-white">{{ previewRecord.prompt }}</p>
          <button type="button" class="rounded-lg p-2 text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-800" :title="t('common.close')" @click="previewRecord = null">
            <Icon name="x" size="sm" />
          </button>
        </header>
        <div class="grid max-h-[calc(92vh-60px)] gap-4 overflow-auto bg-gray-100 p-4 dark:bg-dark-950" :class="previewRecord.images.length > 1 ? 'md:grid-cols-2' : 'grid-cols-1'">
          <a v-for="(_, index) in previewRecord.images" :key="index" :href="displayURL(previewRecord, index)" target="_blank" rel="noopener" class="grid min-h-64 place-items-center">
            <img :src="displayURL(previewRecord, index)" :alt="previewRecord.prompt" class="max-h-[75vh] max-w-full object-contain" />
          </a>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import { useAppStore } from '@/stores'
import { deleteImageHistory, displayImageURL, listImageHistory } from './history'
import type { ImageGenerationHistoryRecord } from './types'

const router = useRouter()
const appStore = useAppStore()
const { t } = useI18n()
const records = ref<ImageGenerationHistoryRecord[]>([])
const loading = ref(false)
const search = ref('')
const previewRecord = ref<ImageGenerationHistoryRecord | null>(null)
const objectURLs = new Map<string, string>()

const filteredRecords = computed(() => {
  const query = search.value.trim().toLowerCase()
  if (!query) return records.value
  return records.value.filter((record) => `${record.prompt} ${record.model} ${record.apiKeyName}`.toLowerCase().includes(query))
})

async function loadHistory() {
  loading.value = true
  try {
    records.value = await listImageHistory()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('imageGeneration.messages.historyLoadFailed'))
  } finally {
    loading.value = false
  }
}

function displayURL(record: ImageGenerationHistoryRecord, index: number) {
  const image = record.images[index]
  if (!image) return ''
  const key = `${record.id}:${index}`
  if (!objectURLs.has(key)) objectURLs.set(key, displayImageURL(image))
  return objectURLs.get(key) || image.url
}

async function remove(id: string) {
  if (!window.confirm(t('imageGeneration.history.deleteConfirm'))) return
  try {
    await deleteImageHistory(id)
    records.value = records.value.filter((record) => record.id !== id)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('imageGeneration.messages.deleteFailed'))
  }
}

function reuse(record: ImageGenerationHistoryRecord) {
  sessionStorage.setItem('image-generation-draft-prompt', record.prompt)
  sessionStorage.setItem('image-generation-draft-model', record.model)
  sessionStorage.setItem('image-generation-draft-size', record.size)
  sessionStorage.setItem('image-generation-draft-quality', record.quality)
  void router.push('/image-generation/create')
}

function formatDate(value: number) {
  return new Date(value).toLocaleString()
}

onMounted(loadHistory)
onBeforeUnmount(() => {
  for (const url of objectURLs.values()) {
    if (url.startsWith('blob:')) URL.revokeObjectURL(url)
  }
})
</script>
