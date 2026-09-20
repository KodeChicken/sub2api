<template>
  <div ref="container" class="relative h-full min-h-[460px] overflow-hidden bg-gray-200 dark:bg-dark-950">
    <div ref="stageHost" class="absolute inset-0" />
    <div v-if="loading" class="absolute inset-0 grid place-items-center bg-white/70 dark:bg-dark-900/70">
      <LoadingSpinner />
    </div>
  </div>
</template>

<script setup lang="ts">
import Konva from 'konva'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { cloneEditorDocument, type LocalImageEditorDocument } from './editor'

const props = defineProps<{ document: LocalImageEditorDocument; imageUrl: string }>()
const emit = defineEmits<{ commit: [document: LocalImageEditorDocument]; zoom: [value: number] }>()
const container = ref<HTMLDivElement | null>(null)
const stageHost = ref<HTMLDivElement | null>(null)
const loading = ref(true)
let stage: Konva.Stage | null = null
let layer: Konva.Layer | null = null
let image: HTMLImageElement | null = null
let observer: ResizeObserver | null = null
let frame = 0
let zoom = 1

onMounted(() => {
  if (!stageHost.value || !container.value) return
  stage = new Konva.Stage({ container: stageHost.value, width: 1, height: 1 })
  layer = new Konva.Layer()
  stage.add(layer)
  stage.on('wheel', handleWheel)
  observer = new ResizeObserver(resize)
  observer.observe(container.value)
  resize()
  loadImage()
})

onBeforeUnmount(() => {
  observer?.disconnect()
  cancelAnimationFrame(frame)
  stage?.destroy()
})

watch(() => props.imageUrl, loadImage)
watch(() => props.document, renderSoon, { deep: true })

function loadImage() {
  loading.value = true
  const next = new Image()
  next.onload = () => {
    image = next
    loading.value = false
    renderSoon()
  }
  next.onerror = () => { loading.value = false }
  next.src = props.imageUrl
}

function resize() {
  if (!stage || !container.value) return
  stage.size({ width: container.value.clientWidth, height: container.value.clientHeight })
  renderSoon()
}

function renderSoon() {
  cancelAnimationFrame(frame)
  frame = requestAnimationFrame(render)
}

function render() {
  if (!stage || !layer) return
  layer.destroyChildren()
  const canvas = props.document.canvas
  const margin = stage.width() < 700 ? 24 : 72
  const fit = Math.min((stage.width() - margin * 2) / canvas.width, (stage.height() - margin * 2) / canvas.height)
  const scale = Math.max(0.02, Math.min(8, fit * zoom))
  emit('zoom', Math.round(scale * 100))
  const workspace = new Konva.Group({
    x: (stage.width() - canvas.width * scale) / 2,
    y: (stage.height() - canvas.height * scale) / 2,
    scaleX: scale,
    scaleY: scale,
  })
  layer.add(workspace)
  workspace.add(new Konva.Rect({
    width: canvas.width,
    height: canvas.height,
    fill: canvas.background.type === 'color' ? canvas.background.color : '#ffffff',
    stroke: '#9ca3af',
    strokeWidth: 1 / scale,
    shadowColor: '#000',
    shadowOpacity: 0.2,
    shadowBlur: 18,
  }))
  if (image) {
    const state = props.document.image
    const crop = state.crop
    if (canvas.background.type === 'blurred-image') {
      const backgroundScale = Math.max(canvas.width / image.width, canvas.height / image.height)
      const background = new Konva.Image({
        image,
        x: (canvas.width - image.width * backgroundScale) / 2,
        y: (canvas.height - image.height * backgroundScale) / 2,
        width: image.width,
        height: image.height,
        scaleX: backgroundScale,
        scaleY: backgroundScale,
        listening: false,
      })
      background.cache()
      background.filters([Konva.Filters.Blur])
      background.blurRadius(canvas.background.blurRadius)
      workspace.add(background)
    }
    const node = new Konva.Image({
      image,
      x: state.x,
      y: state.y,
      width: crop.width,
      height: crop.height,
      crop,
      rotation: state.rotation,
      scaleX: state.flipX ? -state.scaleX : state.scaleX,
      scaleY: state.flipY ? -state.scaleY : state.scaleY,
      offsetX: state.flipX ? crop.width : 0,
      offsetY: state.flipY ? crop.height : 0,
      draggable: true,
    })
    node.on('dragend transformend', () => {
      const next = cloneEditorDocument(props.document)
      next.image.x = node.x()
      next.image.y = node.y()
      next.image.rotation = normalizeRotation(node.rotation())
      next.image.scaleX = Math.abs(node.scaleX())
      next.image.scaleY = Math.abs(node.scaleY())
      emit('commit', next)
    })
    workspace.add(node)
    workspace.add(new Konva.Transformer({
      nodes: [node],
      keepRatio: true,
      flipEnabled: false,
      borderStroke: '#2563eb',
      anchorFill: '#fff',
      anchorStroke: '#2563eb',
      anchorSize: Math.max(7, 10 / scale),
    }))
  }
  layer.draw()
}

function handleWheel(event: Konva.KonvaEventObject<WheelEvent>) {
  event.evt.preventDefault()
  zoom = Math.min(8, Math.max(0.2, zoom * (event.evt.deltaY > 0 ? 0.9 : 1.1)))
  renderSoon()
}

function fitViewport() {
  zoom = 1
  renderSoon()
}

function normalizeRotation(value: number) {
  return ((value % 360) + 360) % 360
}

defineExpose({ fitViewport })
</script>
