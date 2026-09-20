<template>
  <div class="flex min-h-[calc(100vh-13rem)] flex-col overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
    <header class="flex flex-wrap items-center gap-2 border-b border-gray-200 px-3 py-2 dark:border-dark-700">
      <RouterLink to="/image-generation/history" class="btn btn-secondary btn-sm">
        <Icon name="arrowLeft" size="sm" class="mr-1.5" />返回
      </RouterLink>
      <span class="min-w-0 flex-1 truncate text-sm font-medium text-gray-900 dark:text-white">{{ record?.prompt || '图片编辑' }}</span>
      <button class="btn btn-secondary btn-sm" :disabled="!canUndo" @click="undo">撤销</button>
      <button class="btn btn-secondary btn-sm" :disabled="!canRedo" @click="redo">重做</button>
      <button class="btn btn-secondary btn-sm" @click="reset">重置</button>
      <button class="btn btn-primary btn-sm" :disabled="exporting" @click="download">
        <Icon name="download" size="sm" class="mr-1.5" />导出
      </button>
    </header>

    <div v-if="loading" class="grid min-h-[560px] place-items-center"><LoadingSpinner /></div>
    <div v-else-if="!record || !sourceBlob" class="grid min-h-[560px] place-items-center text-sm text-gray-500">图片记录不存在或原图缓存已失效</div>
    <div v-else class="grid min-h-0 flex-1 lg:grid-cols-[minmax(0,1fr)_320px]">
      <ImageEditorCanvas ref="canvasRef" :document="editorDocument" :image-url="sourceURL" @commit="commit" @zoom="zoom = $event" />
      <aside class="max-h-[calc(100vh-16rem)] space-y-5 overflow-y-auto border-t border-gray-200 p-4 dark:border-dark-700 lg:border-l lg:border-t-0">
        <section>
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white">画布</h2>
          <div class="mt-3 grid grid-cols-2 gap-2">
            <label class="input-label">宽度<input v-model.number="canvasWidth" type="number" min="16" max="8192" class="input mt-1" /></label>
            <label class="input-label">高度<input v-model.number="canvasHeight" type="number" min="16" max="8192" class="input mt-1" /></label>
          </div>
          <button class="btn btn-secondary btn-sm mt-2 w-full" @click="applyCanvasSize">应用画布尺寸</button>
          <label class="input-label mt-3 block">背景
            <Select v-model="background" class="mt-1" :options="backgroundOptions" @change="applyBackground" />
          </label>
          <label v-if="background === 'custom'" class="input-label mt-3 block">背景颜色
            <input v-model="backgroundColor" type="color" class="mt-1 h-10 w-full cursor-pointer rounded-md border border-gray-300 bg-white p-1 dark:border-dark-600 dark:bg-dark-800" @input="applyBackground" />
          </label>
          <label v-if="background === 'blurred-image'" class="input-label mt-3 block">模糊强度 {{ blurRadius }}px
            <input v-model.number="blurRadius" type="range" min="0" max="64" step="1" class="mt-2 w-full" @input="applyBackground" />
          </label>
        </section>

        <section class="border-t border-gray-200 pt-4 dark:border-dark-700">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white">图片变换</h2>
          <div class="mt-3 grid grid-cols-2 gap-2">
            <button class="btn btn-secondary btn-sm" @click="fit('contain')">完整适应</button>
            <button class="btn btn-secondary btn-sm" @click="fit('cover')">铺满画布</button>
            <button class="btn btn-secondary btn-sm" @click="rotate(-90)">左转 90°</button>
            <button class="btn btn-secondary btn-sm" @click="rotate(90)">右转 90°</button>
            <button class="btn btn-secondary btn-sm" @click="flip('x')">水平翻转</button>
            <button class="btn btn-secondary btn-sm" @click="flip('y')">垂直翻转</button>
          </div>
        </section>

        <section class="border-t border-gray-200 pt-4 dark:border-dark-700">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white">裁剪</h2>
          <div class="mt-3 grid grid-cols-2 gap-2">
            <label class="input-label">X<input v-model.number="crop.x" type="number" min="0" class="input mt-1" /></label>
            <label class="input-label">Y<input v-model.number="crop.y" type="number" min="0" class="input mt-1" /></label>
            <label class="input-label">宽度<input v-model.number="crop.width" type="number" min="16" class="input mt-1" /></label>
            <label class="input-label">高度<input v-model.number="crop.height" type="number" min="16" class="input mt-1" /></label>
          </div>
          <div class="mt-3 grid grid-cols-3 gap-2">
            <button v-for="preset in cropPresets" :key="preset.label" class="btn btn-secondary btn-sm" @click="applyCropPreset(preset.width, preset.height)">{{ preset.label }}</button>
          </div>
          <button class="btn btn-secondary btn-sm mt-2 w-full" @click="applyCrop">应用裁剪</button>
        </section>

        <section class="border-t border-gray-200 pt-4 dark:border-dark-700">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white">导出设置</h2>
          <div class="mt-3 grid grid-cols-2 gap-2">
            <label class="input-label">格式<Select v-model="exportFormat" class="mt-1" :options="exportFormatOptions" /></label>
            <label class="input-label">倍率<Select v-model="exportScale" class="mt-1" :options="exportScaleOptions" /></label>
          </div>
          <label v-if="exportFormat !== 'png'" class="input-label mt-3 block">压缩质量 {{ exportQuality }}%
            <input v-model.number="exportQuality" type="range" min="10" max="100" step="1" class="mt-2 w-full" />
          </label>
          <p class="mt-2 text-xs text-gray-500">导出尺寸 {{ editorDocument.canvas.width * exportScale }} × {{ editorDocument.canvas.height * exportScale }}</p>
        </section>

        <section class="border-t border-gray-200 pt-4 dark:border-dark-700">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white">AI 扩图</h2>
          <p class="mt-1 text-xs leading-5 text-gray-500">扩大画布后，将透明区域和原图作为参考图带回生图工作台。</p>
          <button class="btn btn-primary btn-sm mt-3 w-full" :disabled="exporting" @click="sendToOutpaint">发送到生图工作台</button>
        </section>

        <p class="border-t border-gray-200 pt-3 text-xs text-gray-500 dark:border-dark-700">画布 {{ editorDocument.canvas.width }} × {{ editorDocument.canvas.height }} · 缩放 {{ zoom }}%</p>
      </aside>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Select from '@/components/common/Select.vue'
import { useAppStore } from '@/stores'
import { getImageHistory, loadImageSessionDraft, saveImageSessionDraft } from './history'
import type { ImageGenerationHistoryRecord } from './types'
import ImageEditorCanvas from './ImageEditorCanvas.vue'
import { cloneEditorDocument, createEditorDocument, cropToAspectRatio, exportEditorImage, exportOutpaintInputs, fitEditorImage, type LocalImageEditorDocument } from './editor'

const route = useRoute()
const router = useRouter()
const appStore = useAppStore()
const loading = ref(true)
const exporting = ref(false)
const record = ref<ImageGenerationHistoryRecord | null>(null)
const sourceBlob = ref<Blob | null>(null)
const sourceURL = ref('')
const editorDocument = ref<LocalImageEditorDocument>(createEditorDocument(1024, 1024))
const initialDocument = ref<LocalImageEditorDocument | null>(null)
const past = ref<LocalImageEditorDocument[]>([])
const future = ref<LocalImageEditorDocument[]>([])
const canvasWidth = ref(1024)
const canvasHeight = ref(1024)
const sourceWidth = ref(1024)
const sourceHeight = ref(1024)
const background = ref<'transparent' | 'white' | 'black' | 'custom' | 'blurred-image'>('transparent')
const backgroundColor = ref('#ffffff')
const blurRadius = ref(24)
const crop = reactive({ x: 0, y: 0, width: 1024, height: 1024 })
const zoom = ref(100)
const exportFormat = ref<'png' | 'jpeg' | 'webp'>('png')
const exportScale = ref<1 | 2 | 3>(1)
const exportQuality = ref(95)
const canvasRef = ref<InstanceType<typeof ImageEditorCanvas> | null>(null)
const canUndo = computed(() => past.value.length > 0)
const canRedo = computed(() => future.value.length > 0)
const backgroundOptions = [
  { value: 'transparent', label: '透明' },
  { value: 'white', label: '白色' },
  { value: 'black', label: '黑色' },
  { value: 'custom', label: '自定义颜色' },
  { value: 'blurred-image', label: '原图模糊' },
]
const exportFormatOptions = [
  { value: 'png', label: 'PNG' },
  { value: 'jpeg', label: 'JPEG' },
  { value: 'webp', label: 'WebP' },
]
const exportScaleOptions = [
  { value: 1, label: '标准 1×' },
  { value: 2, label: '高清 2×' },
  { value: 3, label: '超清 3×' },
]
const cropPresets = [
  { label: '原图', width: 0, height: 0 },
  { label: '1:1', width: 1, height: 1 },
  { label: '4:3', width: 4, height: 3 },
  { label: '3:4', width: 3, height: 4 },
  { label: '16:9', width: 16, height: 9 },
  { label: '9:16', width: 9, height: 16 },
]

onMounted(load)
onBeforeUnmount(() => { if (sourceURL.value) URL.revokeObjectURL(sourceURL.value) })

async function load() {
  try {
    const item = await getImageHistory(String(route.params.recordId || ''))
    const index = Math.max(0, Number(route.params.index || 0))
    const image = item?.images[index]
    if (!item || !image?.blob) return
    record.value = item
    sourceBlob.value = image.blob
    sourceURL.value = URL.createObjectURL(image.blob)
    const width = image.width || await measure(sourceURL.value, 'width')
    const height = image.height || await measure(sourceURL.value, 'height')
    sourceWidth.value = width
    sourceHeight.value = height
    const created = createEditorDocument(width, height)
    editorDocument.value = created
    initialDocument.value = cloneEditorDocument(created)
    syncControls()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '加载图片失败')
  } finally {
    loading.value = false
  }
}

function measure(url: string, key: 'width' | 'height'): Promise<number> {
  return new Promise(resolve => {
    const image = new Image()
    image.onload = () => resolve(image[key])
    image.onerror = () => resolve(1024)
    image.src = url
  })
}

function commit(next: LocalImageEditorDocument) {
  past.value.push(cloneEditorDocument(editorDocument.value))
  if (past.value.length > 100) past.value.shift()
  future.value = []
  editorDocument.value = cloneEditorDocument(next)
  syncControls()
}

function undo() {
  const previous = past.value.pop()
  if (!previous) return
  future.value.push(cloneEditorDocument(editorDocument.value))
  editorDocument.value = previous
  syncControls()
}

function redo() {
  const next = future.value.pop()
  if (!next) return
  past.value.push(cloneEditorDocument(editorDocument.value))
  editorDocument.value = next
  syncControls()
}

function reset() {
  if (initialDocument.value) commit(initialDocument.value)
}

function syncControls() {
  const document = editorDocument.value
  canvasWidth.value = document.canvas.width
  canvasHeight.value = document.canvas.height
  if (document.canvas.background.type === 'transparent') background.value = 'transparent'
  else if (document.canvas.background.type === 'blurred-image') {
    background.value = 'blurred-image'
    blurRadius.value = document.canvas.background.blurRadius
  } else {
    backgroundColor.value = document.canvas.background.color
    background.value = document.canvas.background.color === '#ffffff'
      ? 'white'
      : document.canvas.background.color === '#000000'
        ? 'black'
        : 'custom'
  }
  Object.assign(crop, document.image.crop)
}

function applyCanvasSize() {
  if (!validEdge(canvasWidth.value) || !validEdge(canvasHeight.value)) return appStore.showError('画布宽高必须在 16 到 8192 之间')
  const next = cloneEditorDocument(editorDocument.value)
  next.canvas.width = Math.round(canvasWidth.value)
  next.canvas.height = Math.round(canvasHeight.value)
  commit(next)
  canvasRef.value?.fitViewport()
}

function applyBackground() {
  const next = cloneEditorDocument(editorDocument.value)
  next.canvas.background = background.value === 'transparent'
    ? { type: 'transparent' }
    : background.value === 'blurred-image'
      ? { type: 'blurred-image', blurRadius: Math.max(0, Math.min(64, blurRadius.value)) }
      : { type: 'color', color: background.value === 'white' ? '#ffffff' : background.value === 'black' ? '#000000' : backgroundColor.value }
  commit(next)
}

function fit(mode: 'contain' | 'cover') { commit(fitEditorImage(editorDocument.value, mode)) }
function rotate(amount: number) {
  const next = cloneEditorDocument(editorDocument.value)
  next.image.rotation = (next.image.rotation + amount + 360) % 360
  commit(next)
}
function flip(axis: 'x' | 'y') {
  const next = cloneEditorDocument(editorDocument.value)
  if (axis === 'x') next.image.flipX = !next.image.flipX
  else next.image.flipY = !next.image.flipY
  commit(next)
}

function applyCrop() {
  if (!sourceBlob.value) return
  const next = cloneEditorDocument(editorDocument.value)
  next.image.crop = {
    x: Math.max(0, Math.round(crop.x)),
    y: Math.max(0, Math.round(crop.y)),
    width: Math.max(16, Math.round(crop.width)),
    height: Math.max(16, Math.round(crop.height)),
  }
  if (next.image.crop.x + next.image.crop.width > sourceWidth.value || next.image.crop.y + next.image.crop.height > sourceHeight.value) {
    return appStore.showError('裁剪区域不能超出原图')
  }
  commit(next)
}

function applyCropPreset(width: number, height: number) {
  const nextCrop = width && height
    ? cropToAspectRatio(sourceWidth.value, sourceHeight.value, width, height)
    : { x: 0, y: 0, width: sourceWidth.value, height: sourceHeight.value }
  Object.assign(crop, nextCrop)
  applyCrop()
}

async function exportedBlob() {
  if (!sourceBlob.value) throw new Error('原图不可用')
  if (exportFormat.value === 'jpeg' && editorDocument.value.canvas.background.type === 'transparent') {
    throw new Error('透明背景不能导出 JPEG，请先选择背景色')
  }
  const document = cloneEditorDocument(editorDocument.value)
  document.canvas.width *= exportScale.value
  document.canvas.height *= exportScale.value
  document.image.x *= exportScale.value
  document.image.y *= exportScale.value
  document.image.scaleX *= exportScale.value
  document.image.scaleY *= exportScale.value
  return exportEditorImage(sourceBlob.value, document, `image/${exportFormat.value}`, exportQuality.value / 100)
}

async function download() {
  exporting.value = true
  try {
    const blob = await exportedBlob()
    const url = URL.createObjectURL(blob)
    const link = window.document.createElement('a')
    link.href = url
    const extension = exportFormat.value === 'jpeg' ? 'jpg' : exportFormat.value
    link.download = `sub2api-edited-${Date.now()}.${extension}`
    link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '导出失败')
  } finally {
    exporting.value = false
  }
}

async function sendToOutpaint() {
  exporting.value = true
  try {
    if (!sourceBlob.value) throw new Error('原图不可用')
    const { image, mask } = await exportOutpaintInputs(sourceBlob.value, editorDocument.value)
    const sessionId = localStorage.getItem('image-generation-active-session') || ''
    if (!sessionId) throw new Error('请先创建生图会话')
    const existing = await loadImageSessionDraft(sessionId)
    await saveImageSessionDraft({
      sessionId,
      prompt: existing?.prompt || '自然扩展参考图片的画面内容，保持原图主体、光线、材质和构图风格一致，完整填充透明区域。',
      referenceImages: [{ id: crypto.randomUUID(), name: 'outpaint-source.png', mimeType: 'image/png', blob: image }],
      maskImage: { id: crypto.randomUUID(), name: 'outpaint-mask.png', mimeType: 'image/png', blob: mask },
      updatedAt: Date.now(),
    })
    await router.push('/image-generation/create')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '发送扩图任务失败')
  } finally {
    exporting.value = false
  }
}

function validEdge(value: number) { return Number.isFinite(value) && value >= 16 && value <= 8192 }
</script>
