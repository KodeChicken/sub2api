export interface ImageGenerationParameters {
  size: string
  quality: string
  outputCount: number
	values?: Record<string, string | number | boolean>
}

export interface ImageGenerationPreferences {
  selectedModelByKey: Record<string, string>
  parametersByKeyModel: Record<string, ImageGenerationParameters>
}

const STORAGE_KEY = 'image-generation-preferences-v1'
const BASE_IMAGE_SIZES = ['auto', '1024x1024', '1536x1024', '1024x1536']
const GPT_IMAGE_V2_SIZES = [
  ...BASE_IMAGE_SIZES,
  '1536x864',
  '864x1536',
  '2048x2048',
  '2048x1152',
  '1152x2048',
  '3840x2160',
  '2160x3840',
]

export function isGptImageV2Model(model: string): boolean {
  return /^gpt-image-2(?:$|[.-])/i.test(model.trim())
}

export function imageSizeValues(model: string): string[] {
  return isGptImageV2Model(model) ? GPT_IMAGE_V2_SIZES : BASE_IMAGE_SIZES
}

export function parameterPreferenceKey(apiKeyId: number, model: string): string {
  return `${apiKeyId}:${model}`
}

export function loadImageGenerationPreferences(): ImageGenerationPreferences {
  try {
    const parsed = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}')
    return {
      selectedModelByKey: isRecord(parsed?.selectedModelByKey) ? parsed.selectedModelByKey : {},
      parametersByKeyModel: isRecord(parsed?.parametersByKeyModel) ? parsed.parametersByKeyModel : {},
    }
  } catch {
    return { selectedModelByKey: {}, parametersByKeyModel: {} }
  }
}

export function saveImageGenerationPreferences(preferences: ImageGenerationPreferences): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(preferences))
  } catch {
    // Keep generation usable when browser storage is unavailable.
  }
}

function isRecord(value: unknown): value is Record<string, never> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}
