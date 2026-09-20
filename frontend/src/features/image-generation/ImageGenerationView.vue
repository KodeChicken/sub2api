<template>
  <div class="grid min-h-[680px] overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900 lg:h-[calc(100vh-13rem)] lg:grid-cols-[240px_minmax(0,1fr)_300px]">
    <aside class="border-b border-gray-200 bg-gray-50/70 dark:border-dark-700 dark:bg-dark-900 lg:border-b-0 lg:border-r">
      <div class="flex items-center justify-between border-b border-gray-200 p-3 dark:border-dark-700">
        <span class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('imageGeneration.sessions.title') }}</span>
        <button type="button" class="rounded-lg p-2 text-gray-500 hover:bg-gray-200 hover:text-gray-900 dark:hover:bg-dark-700 dark:hover:text-white" :title="t('imageGeneration.sessions.new')" @click="newSession">
          <Icon name="plus" size="sm" />
        </button>
      </div>
      <div class="max-h-52 space-y-1 overflow-y-auto p-2 lg:max-h-none lg:h-[calc(100%-57px)]">
        <button
          v-for="session in sessions"
          :key="session.id"
          type="button"
          class="w-full rounded-lg px-3 py-2.5 text-left transition-colors"
          :class="session.id === activeSessionId
            ? 'bg-primary-50 text-primary-800 dark:bg-primary-900/25 dark:text-primary-200'
            : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-800'"
          @click="activeSessionId = session.id"
        >
          <span class="block truncate text-sm font-medium">{{ session.title }}</span>
          <span class="mt-1 block text-xs text-gray-400 dark:text-gray-500">{{ formatDate(session.updatedAt) }}</span>
        </button>
      </div>
    </aside>

    <main class="flex min-h-[560px] min-w-0 flex-col bg-gray-50/40 dark:bg-dark-950/30">
      <div ref="messageArea" class="flex-1 space-y-5 overflow-y-auto p-4 md:p-6">
        <div v-if="activeRecords.length === 0 && !generating" class="grid h-full min-h-72 place-items-center text-center">
          <div class="max-w-sm">
            <div class="mx-auto flex h-14 w-14 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-900/25 dark:text-primary-300">
              <Icon name="sparkles" size="lg" />
            </div>
            <h2 class="mt-4 text-base font-semibold text-gray-900 dark:text-white">{{ t('imageGeneration.create.emptyTitle') }}</h2>
            <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('imageGeneration.create.emptyDescription') }}</p>
          </div>
        </div>

        <article v-for="record in activeRecords" :key="record.id" class="space-y-3">
          <div class="ml-auto max-w-2xl rounded-lg bg-primary-600 px-4 py-3 text-sm leading-6 text-white">
            {{ record.prompt }}
          </div>
          <div class="grid gap-3" :class="record.images.length > 1 ? 'sm:grid-cols-2' : 'grid-cols-1'">
            <button
              v-for="(_, index) in record.images"
              :key="index"
              type="button"
              class="group relative grid min-h-64 place-items-center overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900"
              @click="openPreview(record, index)"
            >
              <img :src="displayURL(record, index)" :alt="record.prompt" class="max-h-[520px] w-full object-contain" />
              <span class="absolute bottom-3 right-3 rounded-md bg-black/65 px-2 py-1 text-xs text-white opacity-0 transition-opacity group-hover:opacity-100">{{ t('imageGeneration.create.preview') }}</span>
            </button>
          </div>
          <div class="flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span>{{ record.model }}</span>
            <span>{{ record.size }}</span>
            <span>{{ record.apiKeyName }}</span>
            <button type="button" class="ml-auto text-primary-600 hover:text-primary-700 dark:text-primary-400" @click="reuseRecord(record)">{{ t('imageGeneration.create.reuse') }}</button>
          </div>
        </article>

        <article v-if="generating" class="space-y-3">
          <div class="ml-auto max-w-2xl rounded-lg bg-primary-600 px-4 py-3 text-sm leading-6 text-white">{{ submittedPrompt }}</div>
          <div class="grid min-h-72 place-items-center rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
            <div class="text-center">
              <LoadingSpinner />
              <p class="mt-4 text-sm font-medium text-gray-700 dark:text-gray-200">{{ generationStatus }}</p>
              <p v-if="currentTaskId" class="mt-1 font-mono text-xs text-gray-400">{{ currentTaskId }}</p>
            </div>
          </div>
        </article>
      </div>

      <form class="border-t border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900 md:p-4" @submit.prevent="generate">
        <div v-if="referencePreviewURL" class="mb-3 flex items-center gap-3 rounded-lg border border-gray-200 bg-gray-50 p-2 dark:border-dark-700 dark:bg-dark-800">
          <img :src="referencePreviewURL" alt="" class="h-14 w-14 rounded-md object-cover" />
          <div class="min-w-0 flex-1">
            <p class="truncate text-sm font-medium text-gray-800 dark:text-gray-200">{{ referenceImage?.name }}</p>
            <p class="text-xs text-gray-500">{{ t('imageGeneration.create.referenceAttached') }}</p>
          </div>
          <button type="button" class="rounded-lg p-2 text-gray-500 hover:bg-gray-200 dark:hover:bg-dark-700" :title="t('common.remove')" @click="clearReference">
            <Icon name="x" size="sm" />
          </button>
        </div>
        <TextArea
          v-model="prompt"
          :rows="3"
          :disabled="generating"
          :placeholder="t('imageGeneration.create.promptPlaceholder')"
          class="w-full"
          @keydown.ctrl.enter.prevent="generate"
          @keydown.meta.enter.prevent="generate"
        />
        <div class="mt-3 flex flex-wrap items-center justify-between gap-3">
          <label class="btn btn-secondary btn-sm cursor-pointer" :class="generating ? 'pointer-events-none opacity-60' : ''">
            <Icon name="upload" size="sm" class="mr-1.5" />
            {{ t('imageGeneration.create.addReference') }}
            <input type="file" accept="image/png,image/jpeg,image/webp" class="sr-only" :disabled="generating" @change="selectReference" />
          </label>
          <button type="submit" class="btn btn-primary min-w-28" :disabled="!canGenerate">
            <Icon :name="generating ? 'refresh' : 'sparkles'" size="sm" class="mr-2" :class="generating ? 'animate-spin' : ''" />
            {{ generating ? t('imageGeneration.create.generating') : t('imageGeneration.create.generate') }}
          </button>
        </div>
      </form>
    </main>

    <aside class="border-t border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900 lg:border-l lg:border-t-0">
      <div class="space-y-5 p-4">
        <div>
          <label class="input-label mb-1.5 block">{{ t('imageGeneration.form.apiKey') }}</label>
          <Select v-model="selectedKeyId" :options="keyOptions" :disabled="loadingKeys || generating" searchable />
          <RouterLink v-if="!loadingKeys && imageKeys.length === 0" to="/keys" class="mt-2 inline-flex text-xs text-primary-600 hover:text-primary-700 dark:text-primary-400">
            {{ t('imageGeneration.form.noKey') }}
          </RouterLink>
        </div>
        <div>
          <label class="input-label mb-1.5 block">{{ t('imageGeneration.form.model') }}</label>
          <Select v-model="model" :options="modelOptions" :disabled="!selectedKey || loadingModels || generating" searchable creatable />
          <p v-if="loadingModels" class="input-hint mt-1.5">{{ t('imageGeneration.form.loadingModels') }}</p>
        </div>
        <div>
          <label class="input-label mb-1.5 block">{{ t('imageGeneration.form.size') }}</label>
          <Select v-model="size" :options="sizeOptions" :disabled="generating" />
        </div>
        <div>
          <label class="input-label mb-1.5 block">{{ t('imageGeneration.form.quality') }}</label>
          <Select v-model="quality" :options="qualityOptions" :disabled="generating" />
        </div>
        <div>
          <label class="input-label mb-1.5 block">{{ t('imageGeneration.form.count') }}</label>
          <Select v-model="outputCount" :options="countOptions" :disabled="generating" />
        </div>
        <div class="rounded-lg border border-gray-200 bg-gray-50 p-3 text-xs leading-5 text-gray-500 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-400">
          {{ t('imageGeneration.form.billingHint') }}
        </div>
      </div>
    </aside>

    <div v-if="preview" class="fixed inset-0 z-[100000000] grid place-items-center bg-black/75 p-4" @click.self="preview = null">
      <section class="max-h-[92vh] max-w-[94vw] overflow-hidden rounded-lg bg-white shadow-2xl dark:bg-dark-900">
        <header class="flex items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-dark-700">
          <p class="max-w-3xl truncate pr-4 text-sm font-medium text-gray-900 dark:text-white">{{ preview.prompt }}</p>
          <div class="flex items-center gap-2">
            <a :href="preview.url" download class="btn btn-primary btn-sm">
              <Icon name="download" size="sm" class="mr-1.5" />
              {{ t('imageGeneration.create.download') }}
            </a>
            <button type="button" class="rounded-lg p-2 text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-800" :title="t('common.close')" @click="preview = null">
              <Icon name="x" size="sm" />
            </button>
          </div>
        </header>
        <div class="grid max-h-[calc(92vh-60px)] min-h-72 place-items-center overflow-auto bg-gray-100 p-4 dark:bg-dark-950">
          <img :src="preview.url" :alt="preview.prompt" class="max-h-[78vh] max-w-full object-contain" />
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { keysAPI } from '@/api'
import type { ApiKey } from '@/types'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Select from '@/components/common/Select.vue'
import TextArea from '@/components/common/TextArea.vue'
import { useAppStore } from '@/stores'
import {
  getImageGenerationTask,
  imageResultURLs,
  isLikelyImageModel,
  listImageGenerationModels,
  submitImageGeneration,
} from './api'
import {
  cacheGeneratedImages,
  createImageSession,
  displayImageURL,
  listImageHistory,
  loadImageSessions,
  saveImageHistory,
  saveImageSessions,
} from './history'
import type { ImageGenerationHistoryRecord, ImageGenerationSession } from './types'

const SELECTED_KEY_STORAGE = 'image-generation-selected-key'
const POLL_INTERVAL_MS = 2200
const MAX_POLL_ATTEMPTS = 820

const { t } = useI18n()
const appStore = useAppStore()
const messageArea = ref<HTMLElement | null>(null)
const imageKeys = ref<ApiKey[]>([])
const selectedKeyId = ref<number | null>(null)
const models = ref<Array<{ id: string }>>([])
const model = ref('')
const prompt = ref('')
const size = ref('1024x1024')
const quality = ref('auto')
const outputCount = ref(1)
const loadingKeys = ref(false)
const loadingModels = ref(false)
const generating = ref(false)
const currentTaskId = ref('')
const submittedPrompt = ref('')
const generationStatus = ref('')
const referenceImage = ref<File | null>(null)
const referencePreviewURL = ref('')
const sessions = ref<ImageGenerationSession[]>([])
const activeSessionId = ref('')
const history = ref<ImageGenerationHistoryRecord[]>([])
const objectURLs = new Map<string, string>()
const preview = ref<{ url: string; prompt: string } | null>(null)
let pollController: AbortController | null = null

const imageKeysForGeneration = (keys: ApiKey[]) => keys.filter((key) =>
  key.status === 'active' &&
  key.group?.allow_image_generation === true &&
  (key.group.platform === 'openai' || key.group.platform === 'grok'))

const selectedKey = computed(() => imageKeys.value.find((key) => key.id === selectedKeyId.value) || null)
const activeRecords = computed(() => history.value.filter((record) => record.sessionId === activeSessionId.value).sort((a, b) => a.createdAt - b.createdAt))
const canGenerate = computed(() => !generating.value && !!selectedKey.value && !!model.value.trim() && !!prompt.value.trim())
const keyOptions = computed(() => imageKeys.value.map((key) => ({
  value: key.id,
  label: `${key.name} · ${key.group?.name || t('keys.noGroup')}`,
})))
const modelOptions = computed(() => models.value.map((item) => ({ value: item.id, label: item.id })))
const sizeOptions = computed(() => [
  { value: 'auto', label: t('imageGeneration.options.auto') },
  { value: '1024x1024', label: '1024 × 1024' },
  { value: '1536x1024', label: '1536 × 1024' },
  { value: '1024x1536', label: '1024 × 1536' },
])
const qualityOptions = computed(() => [
  { value: 'auto', label: t('imageGeneration.options.auto') },
  { value: 'low', label: t('imageGeneration.options.low') },
  { value: 'medium', label: t('imageGeneration.options.medium') },
  { value: 'high', label: t('imageGeneration.options.high') },
])
const countOptions = [1, 2, 3, 4].map((value) => ({ value, label: String(value) }))

async function loadKeys() {
  loadingKeys.value = true
  try {
    const response = await keysAPI.list(1, 100, { status: 'active' })
    imageKeys.value = imageKeysForGeneration(response.items)
    const stored = Number(localStorage.getItem(SELECTED_KEY_STORAGE))
    selectedKeyId.value = imageKeys.value.some((key) => key.id === stored) ? stored : imageKeys.value[0]?.id || null
  } catch (error) {
    appStore.showError(errorMessage(error, t('imageGeneration.messages.keysLoadFailed')))
  } finally {
    loadingKeys.value = false
  }
}

async function loadModels() {
  models.value = []
  model.value = ''
  if (!selectedKey.value) return
  loadingModels.value = true
  try {
    const result = await listImageGenerationModels(selectedKey.value.key)
    models.value = result
    const draftModel = sessionStorage.getItem('image-generation-draft-model') || ''
    model.value = result.find((item) => item.id === draftModel)?.id
      || result.find(isLikelyImageModel)?.id
      || result[0]?.id
      || ''
    sessionStorage.removeItem('image-generation-draft-model')
  } catch (error) {
    appStore.showError(errorMessage(error, t('imageGeneration.messages.modelsLoadFailed')))
  } finally {
    loadingModels.value = false
  }
}

async function loadLocalState() {
  sessions.value = loadImageSessions().sort((a, b) => b.updatedAt - a.updatedAt)
  if (sessions.value.length === 0) sessions.value = [createImageSession(t('imageGeneration.sessions.defaultTitle'))]
  activeSessionId.value = sessions.value[0].id
  history.value = await listImageHistory()
}

function newSession() {
  const session = createImageSession(t('imageGeneration.sessions.defaultTitle'))
  sessions.value = [session, ...sessions.value]
  activeSessionId.value = session.id
  saveImageSessions(sessions.value)
  prompt.value = ''
  clearReference()
}

function updateActiveSessionTitle(value: string) {
  const session = sessions.value.find((item) => item.id === activeSessionId.value)
  if (!session) return
  if (activeRecords.value.length === 0 || session.title === t('imageGeneration.sessions.defaultTitle')) {
    session.title = value.trim().replace(/\s+/g, ' ').slice(0, 28) || session.title
  }
  session.updatedAt = Date.now()
  sessions.value = [...sessions.value].sort((a, b) => b.updatedAt - a.updatedAt)
  saveImageSessions(sessions.value)
}

async function generate() {
  if (!canGenerate.value || !selectedKey.value) return
  const currentPrompt = prompt.value.trim()
  const key = selectedKey.value
  generating.value = true
  submittedPrompt.value = currentPrompt
  generationStatus.value = t('imageGeneration.create.submitting')
  currentTaskId.value = ''
  pollController = new AbortController()
  try {
    const task = await submitImageGeneration(key.key, {
      model: model.value,
      prompt: currentPrompt,
      size: size.value,
      quality: quality.value,
      n: outputCount.value,
      referenceImage: referenceImage.value,
    })
    currentTaskId.value = task.id || task.task_id || ''
    if (!currentTaskId.value) throw new Error(t('imageGeneration.messages.invalidTask'))
    generationStatus.value = t('imageGeneration.create.processing')
    const completed = await pollTask(key.key, currentTaskId.value, pollController.signal)
    const urls = imageResultURLs(completed.result)
    if (urls.length === 0) throw new Error(t('imageGeneration.messages.noImage'))
    const now = Date.now()
    const record: ImageGenerationHistoryRecord = {
      id: crypto.randomUUID(),
      sessionId: activeSessionId.value,
      taskId: currentTaskId.value,
      prompt: currentPrompt,
      model: model.value,
      size: size.value,
      quality: quality.value,
      outputCount: outputCount.value,
      apiKeyId: key.id,
      apiKeyName: key.name,
      createdAt: now,
      images: await cacheGeneratedImages(urls),
    }
    await saveImageHistory(record)
    history.value = [record, ...history.value]
    updateActiveSessionTitle(currentPrompt)
    prompt.value = ''
    clearReference()
    appStore.showSuccess(t('imageGeneration.messages.generated'))
  } catch (error) {
    if (!(error instanceof DOMException && error.name === 'AbortError')) {
      appStore.showError(errorMessage(error, t('imageGeneration.messages.generateFailed')))
    }
  } finally {
    generating.value = false
    currentTaskId.value = ''
    pollController = null
    await scrollToBottom()
  }
}

async function pollTask(apiKey: string, taskId: string, signal: AbortSignal) {
  for (let attempt = 0; attempt < MAX_POLL_ATTEMPTS; attempt += 1) {
    if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
    const task = await getImageGenerationTask(apiKey, taskId, signal)
    if (task.status === 'completed') return task
    if (task.status === 'failed') throw new Error(task.error?.message || t('imageGeneration.messages.generateFailed'))
    await new Promise<void>((resolve, reject) => {
      const timer = window.setTimeout(resolve, POLL_INTERVAL_MS)
      signal.addEventListener('abort', () => {
        window.clearTimeout(timer)
        reject(new DOMException('Aborted', 'AbortError'))
      }, { once: true })
    })
  }
  throw new Error(t('imageGeneration.messages.timeout'))
}

function selectReference(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (!file.type.startsWith('image/') || file.size > 20 * 1024 * 1024) {
    appStore.showError(t('imageGeneration.messages.invalidReference'))
    return
  }
  clearReference()
  referenceImage.value = file
  referencePreviewURL.value = URL.createObjectURL(file)
}

function clearReference() {
  if (referencePreviewURL.value) URL.revokeObjectURL(referencePreviewURL.value)
  referencePreviewURL.value = ''
  referenceImage.value = null
}

function displayURL(record: ImageGenerationHistoryRecord, index: number) {
  const image = record.images[index]
  if (!image) return ''
  const key = `${record.id}:${index}`
  if (!objectURLs.has(key)) objectURLs.set(key, displayImageURL(image))
  return objectURLs.get(key) || image.url
}

function openPreview(record: ImageGenerationHistoryRecord, index: number) {
  preview.value = { url: displayURL(record, index), prompt: record.prompt }
}

function reuseRecord(record: ImageGenerationHistoryRecord) {
  prompt.value = record.prompt
  model.value = record.model
  size.value = record.size
  quality.value = record.quality
  outputCount.value = record.outputCount
}

async function scrollToBottom() {
  await nextTick()
  if (messageArea.value) messageArea.value.scrollTop = messageArea.value.scrollHeight
}

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error && error.message ? error.message : fallback
}

function formatDate(value: number) {
  return new Date(value).toLocaleString()
}

watch(selectedKeyId, (value) => {
  if (value) localStorage.setItem(SELECTED_KEY_STORAGE, String(value))
  void loadModels()
})
watch(activeSessionId, scrollToBottom)

onMounted(async () => {
  const draftPrompt = sessionStorage.getItem('image-generation-draft-prompt')
  const draftSize = sessionStorage.getItem('image-generation-draft-size')
  const draftQuality = sessionStorage.getItem('image-generation-draft-quality')
  if (draftPrompt) prompt.value = draftPrompt
  if (draftSize) size.value = draftSize
  if (draftQuality) quality.value = draftQuality
  sessionStorage.removeItem('image-generation-draft-prompt')
  sessionStorage.removeItem('image-generation-draft-size')
  sessionStorage.removeItem('image-generation-draft-quality')
  try {
    await loadLocalState()
  } catch (error) {
    appStore.showError(errorMessage(error, t('imageGeneration.messages.historyLoadFailed')))
  }
  await loadKeys()
  await scrollToBottom()
})

onBeforeUnmount(() => {
  pollController?.abort()
  clearReference()
  for (const url of objectURLs.values()) {
    if (url.startsWith('blob:')) URL.revokeObjectURL(url)
  }
})
</script>
