import type { ImageGenerationParameterValue } from './types'

export interface ImageParameterDefinition {
  key: string
  label: string
  type: 'select' | 'number' | 'boolean'
  default: ImageGenerationParameterValue
  options?: Array<{ value: string | number; label: string }>
  min?: number
  max?: number
  step?: number
  editOnly?: boolean
  visibleWhen?: { key: string; values: ImageGenerationParameterValue[] }
  customSize?: ImageCustomSizeConstraints
}

export interface ImageCustomSizeConstraints {
  edgeMultiple: number
  maxEdge: number
  minPixels: number
  maxPixels: number
  maxAspectRatio: number
}

interface ParsedImageSize {
  width: number
  height: number
}

export interface ImageModelCapabilities {
  supportsEdits: boolean
  maxReferenceImages: number
  supportsStreaming: boolean
  parameters: ImageParameterDefinition[]
}

const option = (value: string, label = value) => ({
  value,
  label: label === value && (value === '3840x2160' || value === '2160x3840')
    ? `${value.replace('x', ' × ')} · 4K`
    : label,
})
const auto = option('auto', '自动')
const qualityOptions = [auto, option('low', '低'), option('medium', '中'), option('high', '高')]
const quality25Options = [...qualityOptions, option('xhigh', '超高'), option('max', '最高')]
const baseSizes = ['auto', '1024x1024', '1536x1024', '1024x1536']
const v2Sizes = [
  ...baseSizes,
  '1536x864', '864x1536', '2048x2048', '2048x1152', '1152x2048', '3840x2160', '2160x3840',
]
const gptImage2CustomSize: ImageCustomSizeConstraints = {
  edgeMultiple: 16,
  maxEdge: 3840,
  minPixels: 655360,
  maxPixels: 8294400,
  maxAspectRatio: 3,
}

function select(key: string, label: string, values: string[], defaultValue = values[0]): ImageParameterDefinition {
  return { key, label, type: 'select', default: defaultValue || '', options: values.map(value => option(value)) }
}

function count(max: number): ImageParameterDefinition {
  return { key: 'n', label: '生成数量', type: 'number', default: 1, min: 1, max, step: 1 }
}

export function imageModelCapabilities(model: string): ImageModelCapabilities {
  const id = model.trim().toLowerCase()
  if (/^gpt-image-2\.5(?:$|[.-])/.test(id)) {
    return {
      supportsEdits: true,
      maxReferenceImages: 16,
      supportsStreaming: true,
      parameters: [
        select('aspect_ratio', '宽高比', ['auto', '1:1', '3:2', '2:3', '16:9', '9:16']),
        { ...select('size', '图片尺寸', v2Sizes, '1024x1024'), customSize: gptImage2CustomSize },
        { key: 'quality', label: '生成质量', type: 'select', default: 'auto', options: quality25Options },
        select('background', '背景', ['auto', 'opaque', 'transparent']),
        select('output_format', '输出格式', ['png', 'jpeg', 'webp']),
        { key: 'output_compression', label: '压缩质量', type: 'number', default: 100, min: 0, max: 100, step: 1, visibleWhen: { key: 'output_format', values: ['jpeg', 'webp'] } },
        select('moderation', '内容审核', ['auto', 'low']),
        { key: 'input_fidelity', label: '参考图保真度', type: 'select', default: 'low', options: [option('low', '低'), option('high', '高')], editOnly: true },
        { key: 'partial_images', label: '过程预览', type: 'number', default: 1, min: 0, max: 3, step: 1 },
        count(10),
      ],
    }
  }
  if (/^gpt-image-2(?:$|-)/.test(id)) {
    return {
      supportsEdits: true,
      maxReferenceImages: 16,
      supportsStreaming: true,
      parameters: [
        select('aspect_ratio', '宽高比', ['auto', '1:1', '3:2', '2:3', '16:9', '9:16']),
        { ...select('size', '图片尺寸', v2Sizes, '1024x1024'), customSize: gptImage2CustomSize },
        { key: 'quality', label: '生成质量', type: 'select', default: 'auto', options: qualityOptions },
        select('background', '背景', ['auto', 'opaque', 'transparent']),
        select('output_format', '输出格式', ['png', 'jpeg', 'webp']),
        { key: 'output_compression', label: '压缩质量', type: 'number', default: 100, min: 0, max: 100, step: 1, visibleWhen: { key: 'output_format', values: ['jpeg', 'webp'] } },
        select('moderation', '内容审核', ['auto', 'low']),
        { key: 'input_fidelity', label: '参考图保真度', type: 'select', default: 'low', options: [option('low', '低'), option('high', '高')], editOnly: true },
        { key: 'partial_images', label: '过程预览', type: 'number', default: 1, min: 0, max: 3, step: 1 },
        count(10),
      ],
    }
  }
  if (id.startsWith('gpt-image-')) {
    return {
      supportsEdits: true,
      maxReferenceImages: 16,
      supportsStreaming: true,
      parameters: [
        select('aspect_ratio', '宽高比', ['auto', '1:1', '3:2', '2:3']),
        select('size', '图片尺寸', baseSizes, '1024x1024'),
        { key: 'quality', label: '生成质量', type: 'select', default: 'auto', options: qualityOptions },
        select('background', '背景', ['auto', 'transparent', 'opaque']),
        select('output_format', '输出格式', ['png', 'jpeg', 'webp']),
        { key: 'output_compression', label: '压缩质量', type: 'number', default: 100, min: 0, max: 100, step: 1, visibleWhen: { key: 'output_format', values: ['jpeg', 'webp'] } },
        select('moderation', '内容审核', ['auto', 'low']),
        { key: 'input_fidelity', label: '参考图保真度', type: 'select', default: 'low', options: [option('low', '低'), option('high', '高')], editOnly: true },
        { key: 'partial_images', label: '过程预览', type: 'number', default: 1, min: 0, max: 3, step: 1 },
        count(10),
      ],
    }
  }
  if (id.includes('grok') && (id.includes('image') || id.includes('imagine'))) {
    return {
      supportsEdits: true,
      maxReferenceImages: 1,
      supportsStreaming: false,
      parameters: [
        select('aspect_ratio', '宽高比', ['auto', '1:1', '3:2', '2:3', '16:9', '9:16']),
        select('resolution', '分辨率', ['1k', '2k'], '1k'),
        count(10),
      ],
    }
  }
  return {
    supportsEdits: true,
    maxReferenceImages: 1,
    supportsStreaming: false,
    parameters: [
      select('size', '图片尺寸', baseSizes, '1024x1024'),
      { key: 'quality', label: '生成质量', type: 'select', default: 'auto', options: qualityOptions },
      count(4),
    ],
  }
}

export function defaultImageParameters(model: string): Record<string, ImageGenerationParameterValue> {
  return Object.fromEntries(imageModelCapabilities(model).parameters.map(parameter => [parameter.key, parameter.default]))
}

export function normalizeImageParameters(
  model: string,
  values: Record<string, ImageGenerationParameterValue>,
  editing: boolean,
): Record<string, ImageGenerationParameterValue> {
  const definitions = imageModelCapabilities(model).parameters.filter(parameter => editing || !parameter.editOnly)
  const normalized: Record<string, ImageGenerationParameterValue> = {}
  for (const definition of definitions) {
    if (definition.key === 'aspect_ratio') continue
    if (definition.visibleWhen && !definition.visibleWhen.values.includes(values[definition.visibleWhen.key])) continue
    const candidate = values[definition.key] ?? definition.default
    if (definition.type === 'select') {
      const allowed = definition.options?.map(item => item.value) || []
      normalized[definition.key] = allowed.includes(candidate as string) || (definition.customSize && validateCustomImageSize(String(candidate), definition.customSize) === '')
        ? candidate
        : definition.default
      continue
    }
    if (definition.type === 'number') {
      const parsed = Number(candidate)
      normalized[definition.key] = Math.min(definition.max ?? parsed, Math.max(definition.min ?? parsed, Number.isFinite(parsed) ? parsed : Number(definition.default)))
      continue
    }
    normalized[definition.key] = Boolean(candidate)
  }
  if (normalized.background === 'transparent' && normalized.output_format === 'jpeg') normalized.output_format = 'png'
  if (editing && model.trim().toLowerCase() === 'gpt-image-1' && normalized.input_fidelity === 'low') {
    delete normalized.input_fidelity
  }
  return normalized
}

export function validateCustomImageSize(value: string, constraints: ImageCustomSizeConstraints): string {
  const match = /^(\d+)x(\d+)$/.exec(value.trim())
  if (!match) return '请输入有效的宽度和高度'
  const width = Number(match[1])
  const height = Number(match[2])
  if (width > constraints.maxEdge || height > constraints.maxEdge) return `单边不能超过 ${constraints.maxEdge} 像素`
  if (width % constraints.edgeMultiple !== 0 || height % constraints.edgeMultiple !== 0) return `宽高必须是 ${constraints.edgeMultiple} 的倍数`
  const pixels = width * height
  if (pixels < constraints.minPixels || pixels > constraints.maxPixels) return `总像素需在 ${constraints.minPixels} 到 ${constraints.maxPixels} 之间`
  if (Math.max(width / height, height / width) > constraints.maxAspectRatio) return `宽高比不能超过 ${constraints.maxAspectRatio}:1`
  return ''
}

export function imageSizeOptionsForAspectRatio(definition: ImageParameterDefinition, aspectRatio: string) {
  if (!aspectRatio || aspectRatio === 'auto') return definition.options || []
  return (definition.options || []).filter(option => {
    const value = String(option.value)
    return value === 'auto' || imageSizeMatchesAspectRatio(value, aspectRatio)
  })
}

export function preferredImageSizeForAspectRatio(
  definition: ImageParameterDefinition,
  aspectRatio: string,
  currentSize: string,
) {
  if (!aspectRatio || aspectRatio === 'auto' || imageSizeMatchesAspectRatio(currentSize, aspectRatio)) return currentSize
  const candidates = (definition.options || [])
    .map(option => String(option.value))
    .filter(value => value !== 'auto' && imageSizeMatchesAspectRatio(value, aspectRatio))
  if (candidates.length === 0) return definition.options?.some(option => option.value === 'auto') ? 'auto' : String(definition.default)
  const current = parseImageSize(currentSize)
  const targetPixels = current ? current.width * current.height : 1024 * 1024
  return candidates.reduce((best, candidate) => {
    const bestSize = parseImageSize(best)!
    const candidateSize = parseImageSize(candidate)!
    const bestDistance = Math.abs(Math.log((bestSize.width * bestSize.height) / targetPixels))
    const candidateDistance = Math.abs(Math.log((candidateSize.width * candidateSize.height) / targetPixels))
    return candidateDistance < bestDistance ? candidate : best
  })
}

export function imageSizeMatchesAspectRatio(size: string, aspectRatio: string) {
  const parsedSize = parseImageSize(size)
  const parsedRatio = parseAspectRatio(aspectRatio)
  if (!parsedSize || !parsedRatio) return false
  const actual = parsedSize.width / parsedSize.height
  const expected = parsedRatio.width / parsedRatio.height
  return Math.abs(actual - expected) / expected <= 0.025
}

function parseImageSize(value: string): ParsedImageSize | null {
  const match = /^(\d+)x(\d+)$/.exec(value.trim())
  if (!match) return null
  const width = Number(match[1])
  const height = Number(match[2])
  return width > 0 && height > 0 ? { width, height } : null
}

function parseAspectRatio(value: string): ParsedImageSize | null {
  const match = /^(\d+):(\d+)$/.exec(value.trim())
  if (!match) return null
  const width = Number(match[1])
  const height = Number(match[2])
  return width > 0 && height > 0 ? { width, height } : null
}
