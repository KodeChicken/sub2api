export interface ImageGenerationModel {
  id: string
  object?: string
  owned_by?: string
}

export interface ImageGenerationTask {
  id: string
  task_id?: string
  status: 'processing' | 'completed' | 'failed' | string
  result?: ImageGenerationResult
  error?: {
    code?: string
    type?: string
    message?: string
  }
  created_at?: number
  completed_at?: number | null
  expires_at?: number
  poll_url?: string
}

export type ImageGenerationParameterValue = string | number | boolean

export interface ImageGenerationReferenceImage {
  id: string
  name: string
  mimeType: string
  blob: Blob
	sourceRecordId?: string
	assetUrl?: string
}

export interface ImageGenerationResult {
  created?: number
  data?: Array<{
    url?: string
    b64_json?: string
    revised_prompt?: string
  }>
}

export interface ImageGenerationHistoryRecord {
  id: string
  sessionId: string
  taskId: string
  prompt: string
  model: string
  size: string
  quality: string
  outputCount: number
	parameters?: Record<string, ImageGenerationParameterValue>
  apiKeyId: number
  apiKeyName: string
  createdAt: number
	completedAt?: number
	durationMs?: number
	status?: 'processing' | 'completed' | 'failed' | 'cancelled'
	error?: string
	parentId?: string
	templateId?: string
	templateTitle?: string
	templatePrompt?: string
	referenceImages?: ImageGenerationReferenceImage[]
	maskImage?: ImageGenerationReferenceImage
  referenceImage?: {
    name: string
    mimeType: string
    blob: Blob
		assetUrl?: string
  }
  images: Array<{
    url: string
    mimeType: string
    blob?: Blob
		width?: number
		height?: number
		fileSizeBytes?: number
		revisedPrompt?: string
  }>
}

export interface ImageGenerationSession {
  id: string
  title: string
  createdAt: number
  updatedAt: number
	sortOrder?: number
}

export interface ImageGenerationSessionDraft {
	sessionId: string
	prompt: string
	referenceImages: ImageGenerationReferenceImage[]
	maskImage?: ImageGenerationReferenceImage
	updatedAt: number
}

export interface ImagePromptTemplate {
	id: string
	title: string
	prompt: string
	description: string
	createdAt: number
	updatedAt: number
}

export interface PromptMaterial {
  id: string
  category: string
  title: string
  description: string
  prompt: string
}
