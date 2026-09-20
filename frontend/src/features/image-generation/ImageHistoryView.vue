<template>
  <div class="space-y-5">
    <div class="grid gap-3 lg:grid-cols-[minmax(220px,1fr)_180px_180px_150px_auto]">
      <SearchInput v-model="search" :placeholder="t('imageGeneration.history.search')" class="w-full" />
      <Select v-model="modelFilter" :options="modelFilterOptions" />
      <Select v-model="keyFilter" :options="keyFilterOptions" />
      <Select v-model="statusFilter" :options="statusFilterOptions" />
      <button type="button" class="btn btn-secondary" :disabled="loading" @click="loadHistory">
        <Icon name="refresh" size="sm" class="mr-2" :class="loading ? 'animate-spin' : ''" />
        {{ t('common.refresh') }}
      </button>
    </div>
    <div class="flex flex-wrap items-end gap-3">
      <label class="input-label">开始日期<input v-model="dateFrom" type="date" class="input mt-1" /></label>
      <label class="input-label">结束日期<input v-model="dateTo" type="date" class="input mt-1" /></label>
      <label class="input-label">图片尺寸<input v-model="sizeFilter" class="input mt-1 w-40" placeholder="例如 1024x1024" /></label>
      <button class="btn btn-secondary btn-sm" @click="resetFilters">重置筛选</button>
      <button v-if="filteredRecords.length" class="btn btn-secondary btn-sm ml-auto" @click="downloadAll(filteredRecords)"><Icon name="download" size="xs" class="mr-1.5" />下载当前结果</button>
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
        <button type="button" class="relative block aspect-square w-full bg-gray-100 dark:bg-dark-900" @click="previewRecord = record">
          <img
            v-if="record.images[0]"
            :src="displayURL(record, 0)"
            :alt="record.prompt"
            class="h-full w-full object-contain"
          />
          <span v-if="record.status && record.status !== 'completed'" class="absolute inset-x-3 bottom-3 rounded-md bg-black/70 px-2 py-1 text-xs text-white">{{ statusLabel(record.status) }}</span>
        </button>
        <div class="space-y-3 p-4">
          <p class="line-clamp-2 text-sm font-medium text-gray-900 dark:text-white">{{ record.prompt }}</p>
          <div class="flex flex-wrap gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span>{{ record.model }}</span>
            <span>{{ record.size }}</span>
            <span v-if="record.images[0]?.width">{{ record.images[0].width }}×{{ record.images[0].height }}</span>
            <span>{{ formatDate(record.createdAt) }}</span>
          </div>
          <div class="flex items-center justify-between gap-2">
            <button type="button" class="btn btn-secondary btn-sm" @click="reuse(record)">
              <Icon name="refresh" size="xs" class="mr-1.5" />
              {{ t('imageGeneration.history.reuse') }}
            </button>
            <button v-if="record.images.length" type="button" class="btn btn-secondary btn-sm" title="下载" @click="downloadRecord(record)"><Icon name="download" size="xs" /></button>
            <RouterLink v-if="record.images.length" :to="`/image-generation/editor/${record.id}/0`" class="btn btn-secondary btn-sm" title="编辑图片"><Icon name="edit" size="xs" /></RouterLink>
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
          <div class="flex items-center gap-2">
            <button class="btn btn-primary btn-sm" @click="downloadRecord(previewRecord)"><Icon name="download" size="xs" class="mr-1.5" />下载全部</button>
            <button type="button" class="rounded-lg p-2 text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-800" :title="t('common.close')" @click="previewRecord = null"><Icon name="x" size="sm" /></button>
          </div>
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
import Select from '@/components/common/Select.vue'
import { useAppStore } from '@/stores'
import { deleteImageHistory, displayImageURL, listImageHistory, saveImageSessionDraft } from './history'
import type { ImageGenerationHistoryRecord } from './types'

const router = useRouter()
const appStore = useAppStore()
const { t } = useI18n()
const records = ref<ImageGenerationHistoryRecord[]>([])
const loading = ref(false)
const search = ref('')
const modelFilter = ref('')
const keyFilter = ref('')
const statusFilter = ref('')
const dateFrom = ref('')
const dateTo = ref('')
const sizeFilter = ref('')
const previewRecord = ref<ImageGenerationHistoryRecord | null>(null)
const objectURLs = new Map<string, string>()

const filteredRecords = computed(() => {
  const query = search.value.trim().toLowerCase()
  return records.value.filter((record) => {
    if (query && !`${record.prompt} ${record.model} ${record.apiKeyName}`.toLowerCase().includes(query)) return false
    if (modelFilter.value && record.model !== modelFilter.value) return false
    if (keyFilter.value && String(record.apiKeyId) !== keyFilter.value) return false
    if (statusFilter.value && (record.status || 'completed') !== statusFilter.value) return false
    if (sizeFilter.value.trim() && record.size.toLowerCase() !== sizeFilter.value.trim().toLowerCase()) return false
    if (dateFrom.value && record.createdAt < new Date(`${dateFrom.value}T00:00:00`).getTime()) return false
    if (dateTo.value && record.createdAt >= new Date(`${dateTo.value}T00:00:00`).getTime() + 86400000) return false
    return true
  })
})
const modelFilterOptions = computed(() => [{ value: '', label: '全部模型' }, ...unique(records.value.map(item => item.model)).map(value => ({ value, label: value }))])
const keyFilterOptions = computed(() => [{ value: '', label: '全部 API 密钥' }, ...Array.from(new Map(records.value.map(item => [String(item.apiKeyId), item.apiKeyName]))).map(([value, label]) => ({ value, label }))])
const statusFilterOptions = [
  { value: '', label: '全部状态' }, { value: 'completed', label: '已完成' }, { value: 'failed', label: '失败' }, { value: 'cancelled', label: '已取消' },
]

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

async function reuse(record: ImageGenerationHistoryRecord) {
  const sessionId = localStorage.getItem('image-generation-active-session') || record.sessionId
  const references = record.referenceImages || (record.referenceImage ? [{ id: `${record.id}:legacy`, ...record.referenceImage }] : [])
  await saveImageSessionDraft({
    sessionId,
    prompt: record.prompt,
    referenceImages: references,
    maskImage: record.maskImage,
    updatedAt: Date.now(),
  })
  sessionStorage.setItem('image-generation-draft-prompt', record.prompt)
  sessionStorage.setItem('image-generation-draft-model', record.model)
  sessionStorage.setItem('image-generation-draft-size', record.size)
  sessionStorage.setItem('image-generation-draft-quality', record.quality)
  sessionStorage.setItem('image-generation-draft-count', String(record.outputCount))
  void router.push('/image-generation/create')
}

function resetFilters() {
  search.value = modelFilter.value = keyFilter.value = statusFilter.value = dateFrom.value = dateTo.value = sizeFilter.value = ''
}

function unique(values: string[]) { return [...new Set(values.filter(Boolean))].sort() }

function statusLabel(status: ImageGenerationHistoryRecord['status']) {
  return status === 'failed' ? '生成失败' : status === 'cancelled' ? '已取消' : '已完成'
}

function downloadRecord(record: ImageGenerationHistoryRecord) { void downloadAll([record]) }

async function downloadAll(items: ImageGenerationHistoryRecord[]) {
  for (const record of items) {
    for (let index = 0; index < record.images.length; index += 1) {
      const image = record.images[index]
      const url = image.blob ? URL.createObjectURL(image.blob) : image.url
      const extension = image.mimeType.includes('jpeg') ? 'jpg' : image.mimeType.includes('webp') ? 'webp' : 'png'
      const link = window.document.createElement('a')
      link.href = url
      link.download = `sub2api-${record.model.replace(/[^a-z0-9.-]+/gi, '-')}-${record.createdAt}-${index + 1}.${extension}`
      link.click()
      if (image.blob) setTimeout(() => URL.revokeObjectURL(url), 1000)
      await new Promise(resolve => setTimeout(resolve, 120))
    }
  }
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
