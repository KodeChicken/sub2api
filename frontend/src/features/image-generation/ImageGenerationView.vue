<template>
  <div class="grid min-h-[680px] overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900 lg:h-[calc(100vh-13rem)] lg:grid-cols-[240px_minmax(0,1fr)_300px]">
    <aside class="flex flex-col border-b border-gray-200 bg-gray-50/70 dark:border-dark-700 dark:bg-dark-900 lg:border-b-0 lg:border-r">
      <div class="flex items-center justify-between border-b border-gray-200 p-3 dark:border-dark-700">
        <span class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('imageGeneration.sessions.title') }}</span>
        <button type="button" class="rounded-lg p-2 text-gray-500 hover:bg-gray-200 hover:text-gray-900 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-dark-700 dark:hover:text-white" :title="t('imageGeneration.sessions.new')" :disabled="generating" @click="newSession">
          <Icon name="plus" size="sm" />
        </button>
      </div>
      <div class="border-b border-gray-200 p-2 dark:border-dark-700">
        <div class="relative">
          <Icon name="search" size="sm" class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-400" />
          <input
            v-model="sessionSearch"
            type="search"
            class="input h-9 w-full pl-8 text-sm"
            :placeholder="t('imageGeneration.sessions.search')"
          />
        </div>
      </div>
      <div class="max-h-52 min-h-0 flex-1 space-y-1 overflow-y-auto p-2 lg:max-h-none">
        <div
          v-for="session in filteredSessions"
          :key="session.id"
          class="group flex min-h-14 items-center rounded-lg transition-colors"
          :class="session.id === activeSessionId
            ? 'bg-primary-50 text-primary-800 dark:bg-primary-900/25 dark:text-primary-200'
            : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-800'"
        >
          <input
            v-if="editingSessionId === session.id"
            v-model="sessionTitleDraft"
            data-testid="session-title-input"
            class="input mx-2 h-9 min-w-0 flex-1 text-sm"
            maxlength="80"
            autofocus
            @click.stop
            @keydown.enter.prevent="saveSessionTitle(session)"
            @keydown.esc.prevent="cancelSessionTitleEdit"
            @blur="saveSessionTitle(session)"
          />
          <button v-else type="button" class="min-w-0 flex-1 px-3 py-2.5 text-left disabled:cursor-not-allowed" :disabled="generating" @click="activeSessionId = session.id">
            <span data-testid="session-title" class="block truncate text-sm font-medium">{{ session.title }}</span>
            <span class="mt-1 block text-xs text-gray-400 dark:text-gray-500">{{ formatDate(session.updatedAt) }}</span>
          </button>
          <div v-if="editingSessionId !== session.id" class="mr-1 flex shrink-0 opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100 sm:group-focus-within:opacity-100">
            <button
              type="button"
              class="rounded-md p-1.5 text-gray-400 hover:bg-white hover:text-gray-700 dark:hover:bg-dark-700 dark:hover:text-gray-200"
              :title="t('imageGeneration.sessions.rename')"
              data-testid="rename-session"
              :disabled="generating"
              @click.stop="beginSessionTitleEdit(session)"
            >
              <Icon name="edit" size="xs" />
            </button>
            <button
              type="button"
              class="rounded-md p-1.5 text-gray-400 hover:bg-white hover:text-red-600 dark:hover:bg-dark-700 dark:hover:text-red-400"
              :title="t('imageGeneration.sessions.delete')"
              data-testid="delete-session"
              :disabled="generating"
              @click.stop="deleteSession(session)"
            >
              <Icon name="trash" size="xs" />
            </button>
          </div>
        </div>
        <p v-if="filteredSessions.length === 0" class="px-3 py-8 text-center text-xs text-gray-400">
          {{ t('imageGeneration.sessions.noMatch') }}
        </p>
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
          <div class="ml-auto max-w-2xl space-y-3 rounded-lg bg-primary-600 p-3 text-sm leading-6 text-white">
            <button
              v-if="record.referenceImage"
              type="button"
              class="block w-full max-w-sm overflow-hidden rounded-md bg-black/10 text-left"
              @click="openReferencePreview(record)"
            >
              <img :src="displayReferenceURL(record)" :alt="t('imageGeneration.create.referenceImage', { name: record.referenceImage.name })" class="max-h-64 w-full object-contain" />
              <span class="block truncate px-2.5 py-1.5 text-xs text-white/80">{{ record.referenceImage.name }}</span>
            </button>
            <p>{{ record.prompt }}</p>
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
          <div class="ml-auto max-w-2xl space-y-3 rounded-lg bg-primary-600 p-3 text-sm leading-6 text-white">
            <button
              v-if="submittedReferencePreviewURL && submittedReferenceImage"
              type="button"
              class="block w-full max-w-sm overflow-hidden rounded-md bg-black/10 text-left"
              @click="openSubmittedReferencePreview"
            >
              <img :src="submittedReferencePreviewURL" :alt="t('imageGeneration.create.referenceImage', { name: submittedReferenceImage.name })" class="max-h-64 w-full object-contain" />
              <span class="block truncate px-2.5 py-1.5 text-xs text-white/80">{{ submittedReferenceImage.name }}</span>
            </button>
            <p>{{ submittedPrompt }}</p>
          </div>
          <div class="grid min-h-72 place-items-center rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
            <div class="text-center">
              <LoadingSpinner />
              <p class="mt-4 text-sm font-medium text-gray-700 dark:text-gray-200">{{ generationStatus }}</p>
              <p v-if="currentTaskId" class="mt-1 font-mono text-xs text-gray-400">{{ currentTaskId }}</p>
            </div>
          </div>
        </article>
      </div>

      <form class="border-t border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900 md:p-4" @paste="pasteReference" @submit.prevent="generate">
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
          @keydown="handlePromptKeydown"
        />
        <div class="mt-3 flex flex-wrap items-center justify-between gap-3">
          <label class="btn btn-secondary btn-sm cursor-pointer" :class="generating ? 'pointer-events-none opacity-60' : ''">
            <Icon name="upload" size="sm" class="mr-1.5" />
            {{ t('imageGeneration.create.addReference') }}
            <input type="file" accept="image/png,image/jpeg,image/webp" class="sr-only" :disabled="generating" @change="selectReference" />
          </label>
          <button v-if="generating" type="button" data-testid="stop-generation" class="btn btn-secondary min-w-28" @click="cancelGeneration">
            <Icon name="x" size="sm" class="mr-2" />
            {{ t('imageGeneration.create.stop') }}
          </button>
          <button v-else type="submit" class="btn btn-primary min-w-28" :disabled="!canGenerate">
            <Icon name="sparkles" size="sm" class="mr-2" />
            {{ t('imageGeneration.create.generate') }}
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
  deleteImageHistory,
  displayImageURL,
  listImageHistory,
  loadImageSessions,
  saveImageHistory,
  saveImageSessions,
} from './history'
import type { ImageGenerationHistoryRecord, ImageGenerationResult, ImageGenerationSession } from './types'

const SELECTED_KEY_STORAGE = 'image-generation-selected-key'
const ACTIVE_SESSION_STORAGE = 'image-generation-active-session'
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
const submittedReferenceImage = ref<File | null>(null)
const submittedReferencePreviewURL = ref('')
const sessions = ref<ImageGenerationSession[]>([])
const activeSessionId = ref('')
const sessionSearch = ref('')
const editingSessionId = ref('')
const sessionTitleDraft = ref('')
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
const filteredSessions = computed(() => {
  const query = sessionSearch.value.trim().toLocaleLowerCase()
  return query
    ? sessions.value.filter((session) => session.title.toLocaleLowerCase().includes(query))
    : sessions.value
})
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
  const storedSessionId = localStorage.getItem(ACTIVE_SESSION_STORAGE)
  activeSessionId.value = sessions.value.some((session) => session.id === storedSessionId)
    ? storedSessionId || sessions.value[0].id
    : sessions.value[0].id
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

function beginSessionTitleEdit(session: ImageGenerationSession) {
  editingSessionId.value = session.id
  sessionTitleDraft.value = session.title
}

function saveSessionTitle(session: ImageGenerationSession) {
  if (editingSessionId.value !== session.id) return
  const title = sessionTitleDraft.value.trim().replace(/\s+/g, ' ')
  if (title) {
    session.title = title.slice(0, 80)
    saveImageSessions(sessions.value)
  }
  editingSessionId.value = ''
}

function cancelSessionTitleEdit() {
  editingSessionId.value = ''
}

async function deleteSession(session: ImageGenerationSession) {
  if (!window.confirm(t('imageGeneration.sessions.deleteConfirm', { title: session.title }))) return
  try {
    const deletedRecords = history.value.filter((record) => record.sessionId === session.id)
    await Promise.all(deletedRecords.map((record) => deleteImageHistory(record.id)))
    deletedRecords.forEach(revokeRecordURLs)
    history.value = history.value.filter((record) => record.sessionId !== session.id)
    sessions.value = sessions.value.filter((item) => item.id !== session.id)
    if (sessions.value.length === 0) sessions.value = [createImageSession(t('imageGeneration.sessions.defaultTitle'))]
    if (activeSessionId.value === session.id) activeSessionId.value = sessions.value[0].id
    saveImageSessions(sessions.value)
  } catch (error) {
    appStore.showError(errorMessage(error, t('imageGeneration.messages.sessionDeleteFailed')))
  }
}

function updateActiveSessionTitle(value: string) {
  const session = sessions.value.find((item) => item.id === activeSessionId.value)
  if (!session) return
  if (isDefaultSessionTitle(session.title)) session.title = titleFromPrompt(value) || session.title
  session.updatedAt = Date.now()
  sessions.value = [...sessions.value].sort((a, b) => b.updatedAt - a.updatedAt)
  saveImageSessions(sessions.value)
}

function isDefaultSessionTitle(title: string) {
  return title === t('imageGeneration.sessions.defaultTitle')
    || title === '新生图会话'
    || title === 'New image session'
}

function titleFromPrompt(value: string) {
  const normalized = value.trim().replace(/\s+/g, ' ')
  const characters = Array.from(normalized)
  return characters.length > 30 ? `${characters.slice(0, 30).join('')}…` : normalized
}

async function generate() {
  if (!canGenerate.value || !selectedKey.value) return
  const currentPrompt = prompt.value.trim()
  const currentReference = referenceImage.value
  const currentReferencePreviewURL = referencePreviewURL.value
  const sessionId = activeSessionId.value
  const key = selectedKey.value
  const controller = new AbortController()
  generating.value = true
  submittedPrompt.value = currentPrompt
  submittedReferenceImage.value = currentReference
  submittedReferencePreviewURL.value = currentReferencePreviewURL
  prompt.value = ''
  referenceImage.value = null
  referencePreviewURL.value = ''
  generationStatus.value = t('imageGeneration.create.submitting')
  currentTaskId.value = ''
  pollController = controller
  updateActiveSessionTitle(currentPrompt)
  try {
    const submission = await submitImageGeneration(key.key, {
      model: model.value,
      prompt: currentPrompt,
      size: size.value,
      quality: quality.value,
      n: outputCount.value,
      referenceImage: currentReference,
    }, controller.signal)
    let result: ImageGenerationResult | undefined
    if (submission.mode === 'async') {
      currentTaskId.value = submission.task.id || submission.task.task_id || ''
      if (!currentTaskId.value) throw new Error(t('imageGeneration.messages.invalidTask'))
      generationStatus.value = t('imageGeneration.create.processing')
      const completed = await pollTask(key.key, currentTaskId.value, controller.signal)
      result = completed.result
    } else {
      result = submission.result
    }
    const urls = imageResultURLs(result)
    if (urls.length === 0) throw new Error(t('imageGeneration.messages.noImage'))
    const now = Date.now()
    const record: ImageGenerationHistoryRecord = {
      id: crypto.randomUUID(),
      sessionId,
      taskId: currentTaskId.value,
      prompt: currentPrompt,
      model: model.value,
      size: size.value,
      quality: quality.value,
      outputCount: outputCount.value,
      apiKeyId: key.id,
      apiKeyName: key.name,
      createdAt: now,
      referenceImage: currentReference ? {
        name: currentReference.name,
        mimeType: currentReference.type,
        blob: currentReference,
      } : undefined,
      images: await cacheGeneratedImages(urls),
    }
    await saveImageHistory(record)
    history.value = [record, ...history.value]
    appStore.showSuccess(t('imageGeneration.messages.generated'))
  } catch (error) {
    if (!prompt.value.trim()) prompt.value = currentPrompt
    if (currentReference && !referenceImage.value) {
      referenceImage.value = currentReference
      referencePreviewURL.value = currentReferencePreviewURL || URL.createObjectURL(currentReference)
      submittedReferencePreviewURL.value = ''
    }
    if (!isAbortError(error)) {
      appStore.showError(errorMessage(error, t('imageGeneration.messages.generateFailed')))
    }
  } finally {
    if (preview.value?.url === submittedReferencePreviewURL.value) preview.value = null
    if (submittedReferencePreviewURL.value) URL.revokeObjectURL(submittedReferencePreviewURL.value)
    generating.value = false
    currentTaskId.value = ''
    pollController = null
    submittedReferenceImage.value = null
    submittedReferencePreviewURL.value = ''
    await scrollToBottom()
  }
}

function cancelGeneration() {
  pollController?.abort()
}

function handlePromptKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return
  event.preventDefault()
  void generate()
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
  attachReference(file)
}

function pasteReference(event: ClipboardEvent) {
  if (generating.value || !event.clipboardData) return
  const file = Array.from(event.clipboardData.files).find((item) => item.type.startsWith('image/'))
    || Array.from(event.clipboardData.items)
      .find((item) => item.kind === 'file' && item.type.startsWith('image/'))
      ?.getAsFile()
  if (!file) return
  event.preventDefault()
  attachReference(file)
}

function attachReference(file: File) {
  if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type) || file.size > 20 * 1024 * 1024) {
    appStore.showError(t('imageGeneration.messages.invalidReference'))
    return
  }
  clearReference()
  referenceImage.value = file.name ? file : new File([file], 'clipboard-image', { type: file.type, lastModified: file.lastModified })
  referencePreviewURL.value = URL.createObjectURL(referenceImage.value)
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

function displayReferenceURL(record: ImageGenerationHistoryRecord) {
  if (!record.referenceImage) return ''
  const key = `${record.id}:reference`
  if (!objectURLs.has(key)) objectURLs.set(key, URL.createObjectURL(record.referenceImage.blob))
  return objectURLs.get(key) || ''
}

function openPreview(record: ImageGenerationHistoryRecord, index: number) {
  preview.value = { url: displayURL(record, index), prompt: record.prompt }
}

function openReferencePreview(record: ImageGenerationHistoryRecord) {
  if (!record.referenceImage) return
  preview.value = {
    url: displayReferenceURL(record),
    prompt: t('imageGeneration.create.referenceImage', { name: record.referenceImage.name }),
  }
}

function openSubmittedReferencePreview() {
  if (!submittedReferenceImage.value || !submittedReferencePreviewURL.value) return
  preview.value = {
    url: submittedReferencePreviewURL.value,
    prompt: t('imageGeneration.create.referenceImage', { name: submittedReferenceImage.value.name }),
  }
}

function revokeRecordURLs(record: ImageGenerationHistoryRecord) {
  for (const [key, url] of objectURLs) {
    if (!key.startsWith(`${record.id}:`)) continue
    if (url.startsWith('blob:')) URL.revokeObjectURL(url)
    objectURLs.delete(key)
  }
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

function isAbortError(error: unknown) {
  return error instanceof DOMException && error.name === 'AbortError'
}

function formatDate(value: number) {
  return new Date(value).toLocaleString()
}

watch(selectedKeyId, (value) => {
  if (value) localStorage.setItem(SELECTED_KEY_STORAGE, String(value))
  void loadModels()
})
watch(activeSessionId, (value) => {
  if (value) localStorage.setItem(ACTIVE_SESSION_STORAGE, value)
  void scrollToBottom()
})

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
  if (submittedReferencePreviewURL.value) URL.revokeObjectURL(submittedReferencePreviewURL.value)
  for (const url of objectURLs.values()) {
    if (url.startsWith('blob:')) URL.revokeObjectURL(url)
  }
})
</script>
