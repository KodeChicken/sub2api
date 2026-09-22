<template>
  <div class="grid min-h-[680px] overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900 lg:h-[calc(100vh-13rem)] lg:grid-cols-[240px_minmax(0,1fr)_300px]">
    <aside class="flex flex-col border-b border-gray-200 bg-gray-50/70 dark:border-dark-700 dark:bg-dark-900 lg:border-b-0 lg:border-r">
      <div class="flex items-center justify-between border-b border-gray-200 p-3 dark:border-dark-700">
        <span class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('imageGeneration.sessions.title') }}</span>
        <button type="button" class="rounded-lg p-2 text-gray-500 hover:bg-gray-200 hover:text-gray-900 dark:hover:bg-dark-700 dark:hover:text-white" :title="t('imageGeneration.sessions.new')" @click="newSession">
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
		<a href="/downloads/sub2api-imagegen.zip" download="sub2api-imagegen.zip" class="mt-2 inline-flex items-center gap-1 px-1 text-xs text-primary-600 hover:underline dark:text-primary-400"><Icon name="download" size="xs" />下载生图技能 ZIP</a>
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
          <button v-else type="button" class="min-w-0 flex-1 px-3 py-2.5 text-left" @click="activeSessionId = session.id">
            <span data-testid="session-title" class="block truncate text-sm font-medium">{{ session.title }}</span>
            <span class="mt-1 block text-xs text-gray-400 dark:text-gray-500">{{ formatDate(session.updatedAt) }}</span>
          </button>
          <div v-if="editingSessionId !== session.id" class="mr-1 flex shrink-0 opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100 sm:group-focus-within:opacity-100">
            <button
              type="button"
              class="rounded-md p-1.5 text-gray-400 hover:bg-white hover:text-gray-700 dark:hover:bg-dark-700 dark:hover:text-gray-200"
              title="上移会话"
              :disabled="generating"
              @click.stop="moveSession(session, -1)"
            ><Icon name="arrowUp" size="xs" /></button>
            <button
              type="button"
              class="rounded-md p-1.5 text-gray-400 hover:bg-white hover:text-gray-700 dark:hover:bg-dark-700 dark:hover:text-gray-200"
              title="下移会话"
              :disabled="generating"
              @click.stop="moveSession(session, 1)"
            ><Icon name="arrowDown" size="xs" /></button>
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
        <div v-if="activeRecords.length === 0" class="grid h-full min-h-72 place-items-center text-center">
          <div class="max-w-sm">
            <div class="mx-auto flex h-14 w-14 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-900/25 dark:text-primary-300">
              <Icon name="sparkles" size="lg" />
            </div>
            <h2 class="mt-4 text-base font-semibold text-gray-900 dark:text-white">{{ t('imageGeneration.create.emptyTitle') }}</h2>
            <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('imageGeneration.create.emptyDescription') }}</p>
          </div>
        </div>

        <article v-for="record in activeRecords" :key="record.id" class="space-y-3">
          <div data-testid="user-message" class="ml-auto w-fit max-w-full space-y-3 break-words rounded-lg border border-gray-200 bg-gray-100 p-3 text-sm leading-6 text-gray-900 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-100 sm:max-w-2xl">
            <div v-if="recordReferenceImages(record).length" class="flex flex-wrap gap-2">
              <button v-for="(reference, index) in recordReferenceImages(record)" :key="reference.id || index" type="button" class="w-40 overflow-hidden rounded-md border border-gray-200 bg-white text-left dark:border-dark-600 dark:bg-dark-900" @click="openReferencePreview(record, index)">
                <img :src="displayReferenceURL(record, index)" :alt="t('imageGeneration.create.referenceImage', { name: reference.name })" class="h-40 w-full object-contain" />
                <span class="block truncate px-2.5 py-1.5 text-xs text-gray-600 dark:text-gray-300">{{ reference.name }}</span>
              </button>
            </div>
            <p>{{ record.prompt }}</p>
          </div>
          <div v-if="record.status === 'failed' || record.status === 'cancelled'" class="rounded-lg border p-4 text-sm" :class="record.status === 'failed' ? 'border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/25 dark:text-red-300' : 'border-gray-200 bg-gray-100 text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300'">
            <p class="font-medium">{{ record.status === 'failed' ? '生成失败' : '已取消生成' }}</p>
            <p v-if="record.error" class="mt-1 text-xs leading-5">{{ record.error }}</p>
          </div>
          <div v-if="record.status === 'processing'" class="grid min-h-72 place-items-center rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
            <div class="text-center">
              <img v-if="record.id === activeGenerationRecordId && partialPreviewURL" :src="partialPreviewURL" alt="生成过程预览" class="mx-auto max-h-80 max-w-full object-contain" />
              <LoadingSpinner v-else />
              <p class="mt-4 text-sm font-medium text-gray-700 dark:text-gray-200">{{ record.id === activeGenerationRecordId ? generationStatus : t('imageGeneration.create.processing') }}</p>
              <p v-if="record.id === activeGenerationRecordId" class="mt-1 text-xs text-gray-400">{{ formatDuration(elapsedMs) }}</p>
              <p v-if="record.taskId" class="mt-1 font-mono text-xs text-gray-400">{{ record.taskId }}</p>
            </div>
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
            <span v-if="record.durationMs">{{ formatDuration(record.durationMs) }}</span>
            <RouterLink v-if="authStore.isAdmin && record.requestId" :to="{ path: '/admin/ops', query: { request_id: record.requestId, open_error_details: '1', error_type: 'upstream' } }" class="text-primary-600 hover:underline dark:text-primary-400">查询日志</RouterLink>
            <div class="ml-auto flex flex-wrap items-center gap-3">
              <button type="button" class="text-primary-600 hover:text-primary-700 dark:text-primary-400" @click="continueFrom(record)">从这里继续</button>
              <button type="button" class="text-primary-600 hover:text-primary-700 dark:text-primary-400" @click="regenerateRecord(record)">重新生成</button>
              <RouterLink v-if="record.images.length" :to="`/image-generation/editor/${record.id}/0`" class="text-primary-600 hover:text-primary-700 dark:text-primary-400">编辑图片</RouterLink>
            </div>
          </div>
          <div v-if="branchSiblings(record).length > 1" class="flex justify-end gap-2 text-xs text-gray-500">
            <button class="rounded-md border border-gray-200 px-2 py-1 dark:border-dark-700" @click="switchBranch(record, -1)">上一分支</button>
            <span class="py-1">{{ branchPosition(record) }} / {{ branchSiblings(record).length }}</span>
            <button class="rounded-md border border-gray-200 px-2 py-1 dark:border-dark-700" @click="switchBranch(record, 1)">下一分支</button>
          </div>
        </article>

      </div>

      <form class="border-t border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900 md:p-4" @paste="pasteReference" @submit.prevent="generate">
        <div v-if="referenceDrafts.length" class="mb-3 grid gap-2 sm:grid-cols-2">
          <div v-for="(reference, index) in referenceDrafts" :key="reference.id" class="flex items-center gap-3 rounded-lg border border-gray-200 bg-gray-50 p-2 dark:border-dark-700 dark:bg-dark-800">
            <img :src="reference.url" alt="" class="h-14 w-14 rounded-md object-cover" />
            <div class="min-w-0 flex-1"><p class="truncate text-sm font-medium text-gray-800 dark:text-gray-200">{{ reference.file.name }}</p><p class="text-xs text-gray-500">参考图 {{ index + 1 }} / {{ modelCapabilities.maxReferenceImages }}</p></div>
            <button type="button" class="rounded-lg p-2 text-gray-500 hover:bg-gray-200 dark:hover:bg-dark-700" :title="t('common.remove')" @click="removeReference(index)"><Icon name="x" size="sm" /></button>
          </div>
        </div>
        <p v-if="maskDraft" class="mb-3 flex items-center gap-2 text-xs text-primary-700 dark:text-primary-300">
          <Icon name="checkCircle" size="xs" />扩图遮罩已就绪
        </p>
        <p v-if="continuationParent" class="mb-2 flex items-center justify-between rounded-lg bg-primary-50 px-3 py-2 text-xs text-primary-700 dark:bg-primary-900/20 dark:text-primary-300"><span>下一次生成将从“{{ continuationParent.prompt }}”继续</span><button type="button" @click="continuationParentId = ''">取消</button></p>
        <TextArea
          v-model="prompt"
          :rows="3"
          :disabled="generating && activeSessionId === generationSessionId"
          :placeholder="t('imageGeneration.create.promptPlaceholder')"
          class="w-full"
          @keydown="handlePromptKeydown"
        />
        <div class="mt-3 flex flex-wrap items-center justify-between gap-3">
          <label class="btn btn-secondary btn-sm cursor-pointer" :class="generating ? 'pointer-events-none opacity-60' : ''">
            <Icon name="upload" size="sm" class="mr-1.5" />
            {{ t('imageGeneration.create.addReference') }}
            <input type="file" accept="image/png,image/jpeg,image/webp" class="sr-only" multiple :disabled="(generating && activeSessionId === generationSessionId) || modelCapabilities.maxReferenceImages === 0" @change="selectReference" />
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
          <div class="mb-1.5 flex items-center justify-between gap-2">
            <label class="input-label">{{ t('imageGeneration.form.template') }}</label>
            <RouterLink to="/image-generation/templates" class="text-xs text-primary-600 hover:text-primary-700 dark:text-primary-400">
              {{ t('imageGeneration.form.manageTemplates') }}
            </RouterLink>
          </div>
          <Select v-model="selectedTemplateId" :options="templateOptions" :disabled="generating" />
          <p v-if="currentTemplate?.description" class="input-hint mt-1.5">{{ currentTemplate.description }}</p>
        </div>
        <div v-for="definition in visibleParameterDefinitions" :key="definition.key">
          <label class="input-label mb-1.5 block">{{ definition.label }}</label>
          <div v-if="definition.type === 'select' && definition.customSize" class="space-y-2">
            <Select :model-value="parameterSelectValue(definition)" :options="parameterOptions(definition)" :disabled="generating" @update:model-value="setSelectParameterValue(definition, $event)" />
            <div v-if="parameterSelectValue(definition) === CUSTOM_SIZE_OPTION" class="grid grid-cols-2 gap-2">
              <label class="input-hint">宽度
                <input :value="customSizeDimension(definition.key, 0)" type="number" class="input mt-1 w-full" :step="definition.customSize.edgeMultiple" :max="definition.customSize.maxEdge" :disabled="generating" @input="setCustomSizeDimension(definition.key, 0, Number(($event.target as HTMLInputElement).value))" />
              </label>
              <label class="input-hint">高度
                <input :value="customSizeDimension(definition.key, 1)" type="number" class="input mt-1 w-full" :step="definition.customSize.edgeMultiple" :max="definition.customSize.maxEdge" :disabled="generating" @input="setCustomSizeDimension(definition.key, 1, Number(($event.target as HTMLInputElement).value))" />
              </label>
              <p v-if="customSizeError(definition)" class="col-span-2 text-xs text-red-600 dark:text-red-400">{{ customSizeError(definition) }}</p>
              <p v-else class="col-span-2 text-xs text-gray-500">宽高需为 {{ definition.customSize.edgeMultiple }} 的倍数，单边不超过 {{ definition.customSize.maxEdge }} 像素。</p>
            </div>
          </div>
          <Select v-else-if="definition.type === 'select'" :model-value="parameterValue(definition.key)" :options="parameterOptions(definition)" :disabled="generating" @update:model-value="setParameterValue(definition.key, $event)" />
          <input v-else-if="definition.type === 'number'" :value="parameterValue(definition.key)" type="number" class="input w-full" :min="definition.min" :max="definition.max" :step="definition.step" :disabled="generating" @input="setParameterValue(definition.key, Number(($event.target as HTMLInputElement).value))" />
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
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { keysAPI } from '@/api'
import type { ApiKey } from '@/types'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Select from '@/components/common/Select.vue'
import TextArea from '@/components/common/TextArea.vue'
import { useAppStore, useAuthStore } from '@/stores'
import {
  cancelImageGenerationTask,
  getImageGenerationTask,
  imageResultURLs,
	isAsyncImageTasksDisabled,
  isLikelyImageModel,
  listImageGenerationModels,
  submitImageGeneration,
	submitAsyncImageGeneration,
  streamImageGeneration,
} from './api'
import {
  cacheGeneratedImages,
  createImageSession,
	deleteImageSession,
  deleteImageSessionDraft,
  displayImageURL,
	getImageHistory,
  listImageHistory,
  loadImageSessions,
  loadImageSessionDraft,
  saveImageHistory,
  saveImageSessionDraft,
  saveImageSessions,
} from './history'
import type {
  ImageGenerationHistoryRecord,
  ImageGenerationParameterValue,
  ImageGenerationReferenceImage,
  ImageGenerationResult,
  ImageGenerationSession,
  ImagePromptTemplate,
} from './types'
import {
  loadImageGenerationPreferences,
  parameterPreferenceKey,
  saveImageGenerationPreferences,
} from './preferences'
import {
  defaultImageParameters,
  imageSizeMatchesAspectRatio,
  imageSizeOptionsForAspectRatio,
  imageModelCapabilities,
  normalizeImageParameters,
  preferredImageSizeForAspectRatio,
  validateCustomImageSize,
  type ImageParameterDefinition,
} from './modelCapabilities'
import { loadImagePromptTemplates } from './templates'

const SELECTED_KEY_STORAGE = 'image-generation-selected-key'
const ACTIVE_SESSION_STORAGE = 'image-generation-active-session'
const BRANCH_STORAGE = 'image-generation-branch-selections-v1'
const POLL_INTERVAL_MS = 2200
const MAX_POLL_ATTEMPTS = 820
const CUSTOM_SIZE_OPTION = '__custom_size__'
const QUALITY_VALUES = ['auto', 'low', 'medium', 'high', 'xhigh', 'max']
const COUNT_VALUES = Array.from({ length: 10 }, (_, index) => index + 1)

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const messageArea = ref<HTMLElement | null>(null)
const imageKeys = ref<ApiKey[]>([])
const selectedKeyId = ref<number | null>(null)
const models = ref<Array<{ id: string }>>([])
const model = ref('')
const prompt = ref('')
const promptTemplates = ref<ImagePromptTemplate[]>([])
const selectedTemplateId = ref('')
const parameterValues = reactive<Record<string, ImageGenerationParameterValue>>({})
const size = computed({ get: () => String(parameterValues.size || '1024x1024'), set: value => { parameterValues.size = value } })
const quality = computed({ get: () => String(parameterValues.quality || 'auto'), set: value => { parameterValues.quality = value } })
const outputCount = computed({ get: () => Number(parameterValues.n || 1), set: value => { parameterValues.n = value } })
const loadingKeys = ref(false)
const loadingModels = ref(false)
const generating = ref(false)
const cancelRequested = ref(false)
const currentTaskId = ref('')
const currentRequestId = ref('')
const activeGenerationRecordId = ref('')
const generationSessionId = ref('')
const generationStatus = ref('')
interface ReferenceDraft { id: string; file: File; url: string; sourceRecordId?: string }
const referenceDrafts = ref<ReferenceDraft[]>([])
const maskDraft = ref<ImageGenerationReferenceImage | null>(null)
const submittedReferences = ref<ReferenceDraft[]>([])
const partialPreviewURL = ref('')
const elapsedMs = ref(0)
const sessions = ref<ImageGenerationSession[]>([])
const activeSessionId = ref('')
const sessionSearch = ref('')
const editingSessionId = ref('')
const sessionTitleDraft = ref('')
const history = ref<ImageGenerationHistoryRecord[]>([])
const continuationParentId = ref('')
const branchSelections = reactive<Record<string, string>>(loadBranchSelections())
const objectURLs = new Map<string, string>()
const preview = ref<{ url: string; prompt: string } | null>(null)
const generationPreferences = loadImageGenerationPreferences()
let pollController: AbortController | null = null
const pendingControllers: AbortController[] = []
let elapsedTimer: number | null = null
let draftTimer: number | null = null
let pendingDraftSettings: { model: string; size: string; quality: string; outputCount: number | null } | null = null

const imageKeysForGeneration = (keys: ApiKey[]) => keys.filter((key) =>
  key.status === 'active' &&
  key.group?.allow_image_generation === true &&
  (key.group.platform === 'openai' || key.group.platform === 'grok'))

const selectedKey = computed(() => imageKeys.value.find((key) => key.id === selectedKeyId.value) || null)
const sessionRecords = computed(() => history.value.filter(record => record.sessionId === activeSessionId.value).sort((a, b) => a.createdAt - b.createdAt))
const activeRecords = computed(() => visibleBranch(sessionRecords.value))
const continuationParent = computed(() => sessionRecords.value.find(record => record.id === continuationParentId.value) || null)
const modelCapabilities = computed(() => imageModelCapabilities(model.value))
const visibleParameterDefinitions = computed(() => modelCapabilities.value.parameters.filter(definition =>
  (!definition.editOnly || referenceDrafts.value.length > 0)
  && (!definition.visibleWhen || definition.visibleWhen.values.includes(parameterValues[definition.visibleWhen.key])),
))
const filteredSessions = computed(() => {
  const query = sessionSearch.value.trim().toLocaleLowerCase()
  return query
    ? sessions.value.filter((session) => session.title.toLocaleLowerCase().includes(query))
    : sessions.value
})
const activeCustomSizeError = computed(() => {
  const definition = modelCapabilities.value.parameters.find(item => item.key === 'size' && item.customSize)
  return definition ? customSizeError(definition) : ''
})
const canGenerate = computed(() => !generating.value && !activeCustomSizeError.value && !!selectedKey.value && !!model.value.trim() && !!prompt.value.trim())
const keyOptions = computed(() => imageKeys.value.map((key) => ({
  value: key.id,
  label: `${key.name} · ${key.group?.name || t('keys.noGroup')}`,
})))
const modelOptions = computed(() => models.value.map((item) => ({ value: item.id, label: item.id })))
const templateOptions = computed(() => [
  { value: '', label: t('imageGeneration.form.noTemplate') },
  ...promptTemplates.value.map(item => ({ value: item.id, label: item.title })),
])
const currentTemplate = computed(() => promptTemplates.value.find(item => item.id === selectedTemplateId.value) || null)

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
    const rememberedModel = generationPreferences.selectedModelByKey[String(selectedKey.value.id)] || ''
    model.value = result.find((item) => item.id === pendingDraftSettings?.model)?.id
      || result.find((item) => item.id === rememberedModel)?.id
      || result.find(isLikelyImageModel)?.id
      || result[0]?.id
      || ''
    applyPendingDraftSettings()
  } catch (error) {
    appStore.showError(errorMessage(error, t('imageGeneration.messages.modelsLoadFailed')))
  } finally {
    loadingModels.value = false
  }
}

async function loadServerState() {
  sessions.value = (await loadImageSessions()).sort((a, b) => (b.sortOrder || b.updatedAt) - (a.sortOrder || a.updatedAt))
  if (sessions.value.length === 0) {
		sessions.value = [createImageSession(t('imageGeneration.sessions.defaultTitle'))]
		await saveImageSessions(sessions.value)
	}
  const storedSessionId = localStorage.getItem(ACTIVE_SESSION_STORAGE)
  activeSessionId.value = sessions.value.some((session) => session.id === storedSessionId)
    ? storedSessionId || sessions.value[0].id
    : sessions.value[0].id
  history.value = await listImageHistory()
	await migrateLocalBranches()
}

function resumePendingTasks() {
	for (const record of history.value.filter(item => item.status === 'processing' && item.taskId)) {
		const key = imageKeys.value.find(item => item.id === record.apiKeyId)
		if (!key) continue
		const controller = new AbortController()
		pendingControllers.push(controller)
		void (async () => {
			try {
				const task = await pollTask(key.key, record.taskId, controller.signal)
				const persisted = await getImageHistory(record.id)
				if (persisted?.status === 'completed') {
					history.value = history.value.map(item => item.id === record.id ? persisted : item)
					return
				}
				if (persisted?.status === 'failed' || persisted?.status === 'cancelled') {
					history.value = history.value.map(item => item.id === record.id ? persisted : item)
					return
				}
				if (record.taskId) throw new Error('异步任务已完成，但会话图片尚未保存')
				const urls = imageResultURLs(task.result)
				if (!urls.length) throw new Error(t('imageGeneration.messages.noImage'))
				const completed: ImageGenerationHistoryRecord = {
					...record, status: 'completed', completedAt: Date.now(),
					images: await cacheGeneratedImages(urls, task.result?.data?.map(item => item.revised_prompt)),
				}
				await saveImageHistory(completed)
				history.value = history.value.map(item => item.id === record.id ? completed : item)
			} catch (error) {
				if (controller.signal.aborted) return
				const failed: ImageGenerationHistoryRecord = { ...record, status: 'failed', error: errorMessage(error, t('imageGeneration.messages.generateFailed')), completedAt: Date.now() }
				await saveImageHistory(failed)
				history.value = history.value.map(item => item.id === record.id ? failed : item)
			}
		})()
	}
}

async function newSession() {
  const session = createImageSession(t('imageGeneration.sessions.defaultTitle'))
	try { await saveImageSessions([session]) } catch (error) { appStore.showError(errorMessage(error, '创建会话失败')); return }
  sessions.value = [session, ...sessions.value]
  activeSessionId.value = session.id
}

async function moveSession(session: ImageGenerationSession, direction: -1 | 1) {
  const index = sessions.value.findIndex(item => item.id === session.id)
  const target = index + direction
  if (index < 0 || target < 0 || target >= sessions.value.length) return
  const next = [...sessions.value]
  ;[next[index], next[target]] = [next[target], next[index]]
  next.forEach((item, itemIndex) => { item.sortOrder = next.length - itemIndex })
  sessions.value = next
	try { await saveImageSessions(next) } catch (error) { appStore.showError(errorMessage(error, '会话排序失败')) }
}

function beginSessionTitleEdit(session: ImageGenerationSession) {
  editingSessionId.value = session.id
  sessionTitleDraft.value = session.title
}

async function saveSessionTitle(session: ImageGenerationSession) {
  if (editingSessionId.value !== session.id) return
  const title = sessionTitleDraft.value.trim().replace(/\s+/g, ' ')
  if (title) {
    session.title = title.slice(0, 80)
		try { await saveImageSessions([session]) } catch (error) { appStore.showError(errorMessage(error, '重命名失败')) }
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
		await deleteImageSession(session.id)
    deletedRecords.forEach(revokeRecordURLs)
    history.value = history.value.filter((record) => record.sessionId !== session.id)
    sessions.value = sessions.value.filter((item) => item.id !== session.id)
    if (sessions.value.length === 0) {
		sessions.value = [createImageSession(t('imageGeneration.sessions.defaultTitle'))]
		await saveImageSessions(sessions.value)
	}
    if (activeSessionId.value === session.id) activeSessionId.value = sessions.value[0].id
  } catch (error) {
    appStore.showError(errorMessage(error, t('imageGeneration.messages.sessionDeleteFailed')))
  }
}

async function updateActiveSessionTitle(value: string, sessionId: string) {
  const session = sessions.value.find((item) => item.id === sessionId)
  if (!session) return
  if (isDefaultSessionTitle(session.title)) session.title = titleFromPrompt(value) || session.title
  session.updatedAt = Date.now()
	await saveImageSessions([session])
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

function applyRememberedModelSettings(modelId: string) {
  if (!selectedKey.value || !modelId) return
	Object.assign(parameterValues, defaultImageParameters(modelId))
  const remembered = generationPreferences.parametersByKeyModel[
    parameterPreferenceKey(selectedKey.value.id, modelId)
  ]
	if (remembered?.values) Object.assign(parameterValues, remembered.values)
	if (parameterValues.background === 'transparent' && parameterValues.output_format === 'jpeg') {
		parameterValues.output_format = 'png'
	}
	size.value = remembered && acceptsSize(remembered.size) ? remembered.size : acceptsSize(size.value) ? size.value : String(parameterValues.size || '1024x1024')
  alignSizeToAspectRatio()
  if (remembered && QUALITY_VALUES.includes(remembered.quality)) quality.value = remembered.quality
  if (remembered && COUNT_VALUES.includes(remembered.outputCount)) outputCount.value = remembered.outputCount
	trimReferenceDrafts()
}

function applyPendingDraftSettings() {
  if (!pendingDraftSettings) return
	if (acceptsSize(pendingDraftSettings.size)) size.value = pendingDraftSettings.size
  alignSizeToAspectRatio()
  if (QUALITY_VALUES.includes(pendingDraftSettings.quality)) quality.value = pendingDraftSettings.quality
  if (pendingDraftSettings.outputCount && COUNT_VALUES.includes(pendingDraftSettings.outputCount)) {
    outputCount.value = pendingDraftSettings.outputCount
  }
  pendingDraftSettings = null
}

function saveCurrentModelSettings() {
  if (!selectedKey.value || !model.value) return
  generationPreferences.parametersByKeyModel[parameterPreferenceKey(selectedKey.value.id, model.value)] = {
    size: size.value,
    quality: quality.value,
    outputCount: outputCount.value,
		values: { ...parameterValues },
  }
  saveImageGenerationPreferences(generationPreferences)
}

async function generate() {
  if (!canGenerate.value || !selectedKey.value) return
  const currentPrompt = prompt.value.trim()
  const sessionId = activeSessionId.value
  const key = selectedKey.value
  const parentId = continuationParentId.value || activeRecords.value.at(-1)?.id || ''
  const currentReferences = [...referenceDrafts.value]
  const currentMask = maskDraft.value
  const parentRecord = sessionRecords.value.find(record => record.id === parentId)
  if (
    parentRecord?.images[0]
    && currentReferences.length < modelCapabilities.value.maxReferenceImages
    && !currentReferences.some(reference => reference.sourceRecordId === parentRecord.id)
  ) {
    try {
      currentReferences.unshift(await referenceDraftFromRecord(parentRecord))
    } catch {
      // Text context is still sufficient when an expired remote image cannot be restored.
    }
  }
  const requestPrompt = buildGenerationPrompt(currentPrompt, parentId, currentTemplate.value?.prompt)
  const requestParameters = normalizeImageParameters(model.value, { ...parameterValues }, currentReferences.length > 0)
  const requestSize = String(requestParameters.size ?? size.value)
  const requestQuality = String(requestParameters.quality ?? quality.value)
  const requestCount = Number(requestParameters.n ?? outputCount.value)
  const controller = new AbortController()
  const startedAt = Date.now()
  generating.value = true
  generationSessionId.value = sessionId
  submittedReferences.value = currentReferences
  prompt.value = ''
  referenceDrafts.value = []
  maskDraft.value = null
  continuationParentId.value = ''
  partialPreviewURL.value = ''
  generationStatus.value = t('imageGeneration.create.submitting')
  currentTaskId.value = ''
	currentRequestId.value = ''
	cancelRequested.value = false
  pollController = controller
  startElapsedTimer(startedAt)
  const recordId = crypto.randomUUID()
  activeGenerationRecordId.value = recordId
  try {
    await updateActiveSessionTitle(currentPrompt, sessionId)
    const request = {
      model: model.value,
      prompt: requestPrompt,
      size: requestSize,
      quality: requestQuality,
      n: requestCount,
      referenceImages: currentReferences.map(item => item.file),
      mask: currentMask ? referenceToFile(currentMask) : undefined,
      parameters: requestParameters,
    }
		const pending: ImageGenerationHistoryRecord = {
			id: recordId, sessionId, taskId: '', prompt: currentPrompt,
			model: model.value, size: requestSize, quality: requestQuality, outputCount: requestCount,
			parameters: requestParameters, apiKeyId: key.id, apiKeyName: key.name, createdAt: startedAt,
			status: 'processing', parentId: parentId || undefined,
			templateId: currentTemplate.value?.id, templateTitle: currentTemplate.value?.title,
			templatePrompt: currentTemplate.value?.prompt,
			referenceImages: currentReferences.map(referenceToHistory), maskImage: snapshotReferenceImage(currentMask), images: [],
		}
		await saveImageHistory(pending)
		history.value = [pending, ...history.value]
    await deleteImageSessionDraft(sessionId)
    let result: ImageGenerationResult | undefined
    try {
		const task = await submitAsyncImageGeneration(key.key, request, sessionId, recordId, controller.signal)
		currentTaskId.value = task.id || task.task_id || ''
		currentRequestId.value = task.request_id || ''
		if (!currentTaskId.value) throw new Error(t('imageGeneration.messages.invalidTask'))
		pending.taskId = currentTaskId.value
		pending.requestId = currentRequestId.value
		generationStatus.value = t('imageGeneration.create.processing')
		result = (await pollTask(key.key, currentTaskId.value, controller.signal)).result
	} catch (error) {
		if (!isAsyncImageTasksDisabled(error)) throw error
		if (modelCapabilities.value.supportsStreaming) {
			generationStatus.value = '模型正在生成图片'
			result = await streamImageGeneration(key.key, request, event => {
				if (event.type.endsWith('.partial_image') && event.url) {
					partialPreviewURL.value = event.url
					generationStatus.value = '模型正在细化图片'
				} else if (event.type.endsWith('.completed')) generationStatus.value = '正在保存最终图片'
			}, controller.signal)
		} else {
			const submission = await submitImageGeneration(key.key, request, controller.signal)
			result = submission.mode === 'sync' ? submission.result : (await pollTask(key.key, submission.task.id, controller.signal)).result
		}
	}
    const urls = imageResultURLs(result)
    if (urls.length === 0) throw new Error(t('imageGeneration.messages.noImage'))
	if (currentTaskId.value) {
		const persisted = await getImageHistory(recordId)
		if (persisted?.status === 'completed') {
			history.value = [persisted, ...history.value.filter(item => item.id !== persisted.id)]
			selectRecordBranch(persisted)
			await deleteImageSessionDraft(sessionId)
			appStore.showSuccess(t('imageGeneration.messages.generated'))
			return
		}
		throw new Error(persisted?.error || '异步任务已完成，但会话图片尚未保存')
	}
    const now = Date.now()
    const record: ImageGenerationHistoryRecord = {
      id: recordId,
      sessionId,
      taskId: currentTaskId.value,
      requestId: currentRequestId.value,
      prompt: currentPrompt,
      model: model.value,
      size: requestSize,
      quality: requestQuality,
      outputCount: requestCount,
      parameters: requestParameters,
      apiKeyId: key.id,
      apiKeyName: key.name,
      createdAt: startedAt,
      completedAt: now,
      durationMs: now - startedAt,
      status: 'completed',
      parentId: parentId || undefined,
      templateId: currentTemplate.value?.id,
      templateTitle: currentTemplate.value?.title,
      templatePrompt: currentTemplate.value?.prompt,
      referenceImages: currentReferences.map(referenceToHistory),
      maskImage: snapshotReferenceImage(currentMask),
      images: await cacheGeneratedImages(urls, result?.data?.map(item => item.revised_prompt)),
    }
    await saveImageHistory(record)
		history.value = [record, ...history.value.filter(item => item.id !== record.id)]
    selectRecordBranch(record)
    await deleteImageSessionDraft(sessionId)
    appStore.showSuccess(t('imageGeneration.messages.generated'))
  } catch (error) {
    const cancelled = isAbortError(error)
    const now = Date.now()
    const record: ImageGenerationHistoryRecord = {
      id: recordId, sessionId, taskId: currentTaskId.value, requestId: currentRequestId.value, prompt: currentPrompt,
      model: model.value, size: requestSize, quality: requestQuality, outputCount: requestCount,
      parameters: requestParameters, apiKeyId: key.id, apiKeyName: key.name, createdAt: startedAt,
      completedAt: now, durationMs: now - startedAt, status: cancelled ? 'cancelled' : 'failed',
      error: cancelled ? '用户停止了生成' : errorMessage(error, t('imageGeneration.messages.generateFailed')),
      parentId: parentId || undefined, templateId: currentTemplate.value?.id,
      templateTitle: currentTemplate.value?.title, templatePrompt: currentTemplate.value?.prompt,
      referenceImages: currentReferences.map(referenceToHistory), maskImage: snapshotReferenceImage(currentMask), images: [],
    }
		if (!(cancelled && currentTaskId.value && !cancelRequested.value)) {
			await saveImageHistory(record)
			history.value = [record, ...history.value.filter(item => item.id !== record.id)]
		}
    selectRecordBranch(record)
    if (activeSessionId.value === sessionId) {
      if (!prompt.value.trim()) prompt.value = currentPrompt
      referenceDrafts.value = currentReferences
      maskDraft.value = currentMask
    } else {
      await saveImageSessionDraft({ sessionId, prompt: currentPrompt, referenceImages: currentReferences.map(referenceToHistory), maskImage: snapshotReferenceImage(currentMask), updatedAt: now })
    }
    submittedReferences.value = []
    if (!cancelled) {
      appStore.showError(errorMessage(error, t('imageGeneration.messages.generateFailed')))
    }
  } finally {
    stopElapsedTimer()
    submittedReferences.value.forEach((reference) => {
      if (!referenceDrafts.value.some(draft => draft.id === reference.id)) URL.revokeObjectURL(reference.url)
    })
    generating.value = false
    generationSessionId.value = ''
    activeGenerationRecordId.value = ''
    currentTaskId.value = ''
	currentRequestId.value = ''
    pollController = null
    submittedReferences.value = []
    partialPreviewURL.value = ''
    await scrollToBottom()
  }
}

async function cancelGeneration() {
	cancelRequested.value = true
  if (currentTaskId.value && selectedKey.value) {
    generationStatus.value = '正在取消任务'
    await cancelImageGenerationTask(selectedKey.value.key, currentTaskId.value).catch(() => undefined)
  }
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
    if (task.status === 'cancelled') throw new DOMException('Aborted', 'AbortError')
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
  const files = Array.from(input.files || [])
  input.value = ''
  attachReferences(files)
}

function pasteReference(event: ClipboardEvent) {
  if ((generating.value && activeSessionId.value === generationSessionId.value) || !event.clipboardData) return
  const files = Array.from(event.clipboardData.files).filter(item => item.type.startsWith('image/'))
  if (files.length === 0) {
    for (const item of Array.from(event.clipboardData.items)) {
      if (item.kind === 'file' && item.type.startsWith('image/')) {
        const file = item.getAsFile()
        if (file) files.push(file)
      }
    }
  }
  if (files.length === 0) return
  event.preventDefault()
  attachReferences(files)
}

function attachReferences(files: File[]) {
  if (modelCapabilities.value.maxReferenceImages <= 0) {
    appStore.showError('当前模型不支持参考图')
    return
  }
  const slots = modelCapabilities.value.maxReferenceImages - referenceDrafts.value.length
  for (const file of files.slice(0, Math.max(0, slots))) {
    if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type) || file.size > 20 * 1024 * 1024) {
      appStore.showError(t('imageGeneration.messages.invalidReference'))
      continue
    }
    const normalized = file.name ? file : new File([file], `clipboard-image-${Date.now()}.png`, { type: file.type, lastModified: file.lastModified })
    referenceDrafts.value.push({ id: crypto.randomUUID(), file: normalized, url: URL.createObjectURL(normalized) })
  }
  if (files.length > slots) appStore.showError(`当前模型最多支持 ${modelCapabilities.value.maxReferenceImages} 张参考图`)
  scheduleDraftSave()
}

function removeReference(index: number) {
  const reference = referenceDrafts.value[index]
  if (reference) URL.revokeObjectURL(reference.url)
  referenceDrafts.value.splice(index, 1)
  if (index === 0) maskDraft.value = null
  scheduleDraftSave()
}

function clearReferences() {
  referenceDrafts.value.forEach(reference => URL.revokeObjectURL(reference.url))
  referenceDrafts.value = []
  maskDraft.value = null
}

function displayURL(record: ImageGenerationHistoryRecord, index: number) {
  const image = record.images[index]
  if (!image) return ''
  const key = `${record.id}:${index}`
  if (!objectURLs.has(key)) objectURLs.set(key, displayImageURL(image))
  return objectURLs.get(key) || image.url
}

function recordReferenceImages(record: ImageGenerationHistoryRecord): ImageGenerationReferenceImage[] {
  if (record.referenceImages?.length) return record.referenceImages
  return record.referenceImage ? [{ id: `${record.id}:legacy`, ...record.referenceImage }] : []
}

function displayReferenceURL(record: ImageGenerationHistoryRecord, index: number) {
  const reference = recordReferenceImages(record)[index]
  if (!reference) return ''
  const key = `${record.id}:reference:${index}`
  if (!objectURLs.has(key)) objectURLs.set(key, URL.createObjectURL(reference.blob))
  return objectURLs.get(key) || ''
}

function openPreview(record: ImageGenerationHistoryRecord, index: number) {
  preview.value = { url: displayURL(record, index), prompt: record.prompt }
}

function openReferencePreview(record: ImageGenerationHistoryRecord, index: number) {
  const reference = recordReferenceImages(record)[index]
  if (!reference) return
  preview.value = {
    url: displayReferenceURL(record, index),
    prompt: t('imageGeneration.create.referenceImage', { name: reference.name }),
  }
}

function revokeRecordURLs(record: ImageGenerationHistoryRecord) {
  for (const [key, url] of objectURLs) {
    if (!key.startsWith(`${record.id}:`)) continue
    if (url.startsWith('blob:')) URL.revokeObjectURL(url)
    objectURLs.delete(key)
  }
}

function restoreRecord(record: ImageGenerationHistoryRecord) {
  prompt.value = record.prompt
  model.value = record.model
  selectedTemplateId.value = record.templateId && promptTemplates.value.some(item => item.id === record.templateId)
    ? record.templateId
    : ''
  Object.assign(parameterValues, record.parameters || { size: record.size, quality: record.quality, n: record.outputCount })
  alignSizeToAspectRatio()
  clearReferences()
  referenceDrafts.value = recordReferenceImages(record).map(reference => ({
    id: crypto.randomUUID(),
    file: new File([reference.blob], reference.name, { type: reference.mimeType }),
    url: URL.createObjectURL(reference.blob),
		sourceRecordId: reference.sourceRecordId,
  }))
  maskDraft.value = record.maskImage || null
  scheduleDraftSave()
}

async function continueFrom(record: ImageGenerationHistoryRecord) {
  model.value = record.model
  Object.assign(parameterValues, record.parameters || { size: record.size, quality: record.quality, n: record.outputCount })
  alignSizeToAspectRatio()
  selectedTemplateId.value = record.templateId && promptTemplates.value.some(item => item.id === record.templateId)
    ? record.templateId
    : ''
  clearReferences()
  prompt.value = ''
  continuationParentId.value = record.id
  const image = record.images[0]
  if (image && modelCapabilities.value.maxReferenceImages > 0) {
    try {
      referenceDrafts.value = [await referenceDraftFromRecord(record)]
    } catch (error) {
      appStore.showError(errorMessage(error, t('imageGeneration.messages.contextImageLoadFailed')))
    }
  }
  scheduleDraftSave()
}

async function regenerateRecord(record: ImageGenerationHistoryRecord) {
  restoreRecord(record)
  continuationParentId.value = record.parentId || ''
  await nextTick()
  await generate()
}

function parameterValue(key: string) {
  return parameterValues[key]
}

function setParameterValue(key: string, value: unknown) {
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    parameterValues[key] = value
    if (key === 'aspect_ratio' && typeof value === 'string') alignSizeToAspectRatio(value)
    if (key === 'background' && value === 'transparent' && parameterValues.output_format === 'jpeg') {
      parameterValues.output_format = 'png'
    }
  }
}

function parameterOptions(definition: ImageParameterDefinition) {
  let options = definition.key === 'size'
    ? imageSizeOptionsForAspectRatio(definition, String(parameterValues.aspect_ratio || 'auto'))
    : definition.options || []
  if (definition.key === 'output_format' && parameterValues.background === 'transparent') {
    options = options.filter(option => option.value !== 'jpeg')
  }
  return definition.customSize
    ? [...options, { value: CUSTOM_SIZE_OPTION, label: '自定义尺寸' }]
    : options
}

function parameterSelectValue(definition: ImageParameterDefinition) {
  const value = String(parameterValue(definition.key) || '')
  return definition.options?.some(option => String(option.value) === value) ? value : CUSTOM_SIZE_OPTION
}

function setSelectParameterValue(definition: ImageParameterDefinition, value: unknown) {
  if (value === CUSTOM_SIZE_OPTION) {
    setParameterValue(definition.key, defaultCustomSize(String(parameterValues.aspect_ratio || 'auto')))
    return
  }
  setParameterValue(definition.key, value)
}

function customSizeDimension(key: string, index: 0 | 1) {
  return Number(String(parameterValue(key) || '').split('x')[index]) || 0
}

function setCustomSizeDimension(key: string, index: 0 | 1, value: number) {
  const dimensions: [number, number] = [customSizeDimension(key, 0), customSizeDimension(key, 1)]
  dimensions[index] = Math.max(0, Math.floor(value || 0))
  setParameterValue(key, `${dimensions[0]}x${dimensions[1]}`)
}

function customSizeError(definition: ImageParameterDefinition) {
  if (!definition.customSize || parameterSelectValue(definition) !== CUSTOM_SIZE_OPTION) return ''
  const value = String(parameterValue(definition.key) || '')
  const validationError = validateCustomImageSize(value, definition.customSize)
  if (validationError) return validationError
  const aspectRatio = String(parameterValues.aspect_ratio || 'auto')
  return aspectRatio !== 'auto' && !imageSizeMatchesAspectRatio(value, aspectRatio)
    ? `尺寸需要匹配 ${aspectRatio} 宽高比`
    : ''
}

function acceptsSize(value: string) {
  const definition = modelCapabilities.value.parameters.find(item => item.key === 'size')
  if (!definition) return false
  if (definition.options?.some(option => String(option.value) === value)) return true
  return !!definition.customSize && validateCustomImageSize(value, definition.customSize) === ''
}

function alignSizeToAspectRatio(aspectRatio = String(parameterValues.aspect_ratio || 'auto')) {
  const definition = modelCapabilities.value.parameters.find(item => item.key === 'size')
  if (!definition) return
  const currentSize = String(parameterValues.size || definition.default)
  parameterValues.size = preferredImageSizeForAspectRatio(definition, aspectRatio, currentSize)
}

function defaultCustomSize(aspectRatio: string) {
  return ({
    '1:1': '1280x1280',
    '3:2': '1200x800',
    '2:3': '800x1200',
    '16:9': '1280x720',
    '9:16': '720x1280',
  } as Record<string, string>)[aspectRatio] || '1280x1024'
}

function trimReferenceDrafts() {
  const limit = modelCapabilities.value.maxReferenceImages
  if (referenceDrafts.value.length <= limit) return
  referenceDrafts.value.slice(limit).forEach(reference => URL.revokeObjectURL(reference.url))
  referenceDrafts.value = referenceDrafts.value.slice(0, limit)
}

function referenceToHistory(reference: ReferenceDraft): ImageGenerationReferenceImage {
  return {
		id: reference.id,
		name: reference.file.name,
		mimeType: reference.file.type,
		blob: reference.file,
		sourceRecordId: reference.sourceRecordId,
	}
}

function referenceToFile(reference: ImageGenerationReferenceImage) {
  return new File([reference.blob], reference.name, { type: reference.mimeType })
}

function snapshotReferenceImage(reference: ImageGenerationReferenceImage | null | undefined) {
  if (!reference) return undefined
  return {
    id: reference.id,
    name: reference.name,
    mimeType: reference.mimeType,
    blob: reference.blob,
    sourceRecordId: reference.sourceRecordId,
  }
}

function buildGenerationPrompt(current: string, parentId: string, stylePrompt?: string) {
  const context: string[] = []
  let currentParentId = parentId
  const byId = new Map(sessionRecords.value.map(record => [record.id, record]))
  while (currentParentId && context.length < 6) {
    const record = byId.get(currentParentId)
    if (!record) break
    context.unshift(record.prompt)
    currentParentId = record.parentId || ''
  }
  let result = context.length
    ? `Conversation context:\n${context.map(item => `User: ${item}\nAssistant: Image generated.`).join('\n')}\nCurrent request: ${current}`
    : current
  if (stylePrompt?.trim()) result += `\nStyle guidance: ${stylePrompt.trim()}`
  return result
}

async function fetchImageBlob(url: string) {
  const response = await fetch(url)
  if (!response.ok) throw new Error(`HTTP ${response.status}`)
  return response.blob()
}

async function referenceDraftFromRecord(record: ImageGenerationHistoryRecord): Promise<ReferenceDraft> {
  const image = record.images[0]
  if (!image) throw new Error('Generated image is unavailable')
  const blob = image.blob || await fetchImageBlob(image.url)
  const extension = blob.type.includes('jpeg') ? 'jpg' : blob.type.includes('webp') ? 'webp' : 'png'
  const file = new File([blob], `continuation-${record.id}.${extension}`, { type: blob.type || image.mimeType || 'image/png' })
  return { id: crypto.randomUUID(), file, url: URL.createObjectURL(file), sourceRecordId: record.id }
}

function branchKey(parentId?: string) {
  return parentId || 'root'
}

function loadBranchSelections(): Record<string, string> {
  try {
    const parsed = JSON.parse(localStorage.getItem(BRANCH_STORAGE) || '{}')
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {}
  } catch {
    return {}
  }
}

function saveBranchSelections() {
  localStorage.setItem(BRANCH_STORAGE, JSON.stringify(branchSelections))
}

function visibleBranch(records: ImageGenerationHistoryRecord[]) {
  if (records.length <= 1) return records
  const children = new Map<string, ImageGenerationHistoryRecord[]>()
  for (const record of records) {
    const key = branchKey(record.parentId)
    children.set(key, [...(children.get(key) || []), record])
  }
  const result: ImageGenerationHistoryRecord[] = []
  let parentKey = 'root'
  const visited = new Set<string>()
  while (true) {
    const siblings = (children.get(parentKey) || []).sort((a, b) => a.createdAt - b.createdAt)
    if (siblings.length === 0) break
    const selectedId = branchSelections[`${activeSessionId.value}:${parentKey}`]
    const selected = siblings.find(item => item.id === selectedId) || siblings.at(-1)!
    if (visited.has(selected.id)) break
    visited.add(selected.id)
    result.push(selected)
    parentKey = selected.id
  }
  return result.length ? result : records
}

function branchSiblings(record: ImageGenerationHistoryRecord) {
  return sessionRecords.value.filter(item => branchKey(item.parentId) === branchKey(record.parentId)).sort((a, b) => a.createdAt - b.createdAt)
}

function branchPosition(record: ImageGenerationHistoryRecord) {
  return branchSiblings(record).findIndex(item => item.id === record.id) + 1
}

function switchBranch(record: ImageGenerationHistoryRecord, direction: -1 | 1) {
  const siblings = branchSiblings(record)
  const current = siblings.findIndex(item => item.id === record.id)
  const next = siblings[(current + direction + siblings.length) % siblings.length]
  if (!next) return
  branchSelections[`${activeSessionId.value}:${branchKey(record.parentId)}`] = next.id
  saveBranchSelections()
}

function selectRecordBranch(record: ImageGenerationHistoryRecord) {
  branchSelections[`${record.sessionId}:${branchKey(record.parentId)}`] = record.id
  saveBranchSelections()
}

async function migrateLocalBranches() {
  for (const session of sessions.value) {
    const records = history.value.filter(record => record.sessionId === session.id).sort((a, b) => a.createdAt - b.createdAt)
    if (records.length < 2 || records.some(record => record.parentId)) continue
    for (let index = 1; index < records.length; index += 1) {
      records[index].parentId = records[index - 1].id
      await saveImageHistory(records[index])
    }
  }
}

function startElapsedTimer(startedAt: number) {
  stopElapsedTimer()
  elapsedMs.value = 0
  elapsedTimer = window.setInterval(() => { elapsedMs.value = Date.now() - startedAt }, 250)
}

function stopElapsedTimer() {
  if (elapsedTimer !== null) window.clearInterval(elapsedTimer)
  elapsedTimer = null
}

function formatDuration(value: number) {
  const seconds = Math.max(0, Math.floor(value / 1000))
  const minutes = Math.floor(seconds / 60)
  return minutes ? `${minutes} 分 ${seconds % 60} 秒` : `${seconds} 秒`
}

function scheduleDraftSave() {
  if (draftTimer !== null) window.clearTimeout(draftTimer)
  const sessionId = activeSessionId.value
  draftTimer = window.setTimeout(() => {
    if (sessionId === activeSessionId.value) saveDraftInBackground(sessionId)
  }, 300)
}

function saveDraftInBackground(sessionId: string) {
  void persistDraft(sessionId).catch(error => console.warn('[ImageGeneration] Failed to save draft', error))
}

async function persistDraft(sessionId: string) {
  if (!sessionId || (generating.value && sessionId === generationSessionId.value)) return
  await saveImageSessionDraft({
    sessionId,
    prompt: prompt.value,
    referenceImages: referenceDrafts.value.map(referenceToHistory),
    maskImage: snapshotReferenceImage(maskDraft.value),
    updatedAt: Date.now(),
  })
}

async function loadDraft(sessionId: string) {
  const draft = await loadImageSessionDraft(sessionId)
  if (sessionId !== activeSessionId.value) return
  clearReferences()
  prompt.value = ''
  continuationParentId.value = ''
  if (!draft) return
  prompt.value = draft.prompt
  referenceDrafts.value = draft.referenceImages.slice(0, modelCapabilities.value.maxReferenceImages).map(reference => ({
    id: reference.id || crypto.randomUUID(),
    file: new File([reference.blob], reference.name, { type: reference.mimeType }),
    url: URL.createObjectURL(reference.blob),
		sourceRecordId: reference.sourceRecordId,
  }))
  maskDraft.value = draft.maskImage || null
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
watch(model, (value) => {
  if (!selectedKey.value || !value) return
  generationPreferences.selectedModelByKey[String(selectedKey.value.id)] = value
  applyRememberedModelSettings(value)
  saveImageGenerationPreferences(generationPreferences)
}, { flush: 'sync' })
watch(parameterValues, saveCurrentModelSettings, { deep: true })
watch(prompt, scheduleDraftSave)
watch(activeSessionId, async (value, previous) => {
  if (previous && sessions.value.some(session => session.id === previous)) await persistDraft(previous)
  if (value) localStorage.setItem(ACTIVE_SESSION_STORAGE, value)
  if (value) await loadDraft(value)
  void scrollToBottom()
})

onMounted(async () => {
  promptTemplates.value = loadImagePromptTemplates()
  const draftPrompt = sessionStorage.getItem('image-generation-draft-prompt')
  const draftTemplateId = sessionStorage.getItem('image-generation-draft-template-id') || ''
  const draftModel = sessionStorage.getItem('image-generation-draft-model') || ''
  const draftSize = sessionStorage.getItem('image-generation-draft-size')
  const draftQuality = sessionStorage.getItem('image-generation-draft-quality')
  const draftCount = Number(sessionStorage.getItem('image-generation-draft-count'))
  pendingDraftSettings = {
    model: draftModel,
    size: draftSize || '',
    quality: draftQuality || '',
    outputCount: COUNT_VALUES.includes(draftCount) ? draftCount : null,
  }
  sessionStorage.removeItem('image-generation-draft-prompt')
  sessionStorage.removeItem('image-generation-draft-template-id')
  sessionStorage.removeItem('image-generation-draft-model')
  sessionStorage.removeItem('image-generation-draft-size')
  sessionStorage.removeItem('image-generation-draft-quality')
  sessionStorage.removeItem('image-generation-draft-count')
  try {
    await loadServerState()
  } catch (error) {
    appStore.showError(errorMessage(error, t('imageGeneration.messages.historyLoadFailed')))
  }
  await loadKeys()
	resumePendingTasks()
	if (draftPrompt) prompt.value = draftPrompt
  if (promptTemplates.value.some(item => item.id === draftTemplateId)) selectedTemplateId.value = draftTemplateId
  await scrollToBottom()
})

onBeforeUnmount(() => {
  pollController?.abort()
	pendingControllers.forEach(controller => controller.abort())
	stopElapsedTimer()
	if (draftTimer !== null) window.clearTimeout(draftTimer)
	saveDraftInBackground(activeSessionId.value)
  clearReferences()
	submittedReferences.value.forEach(reference => URL.revokeObjectURL(reference.url))
  for (const url of objectURLs.values()) {
    if (url.startsWith('blob:')) URL.revokeObjectURL(url)
  }
})
</script>
