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
  apiKeyId: number
  apiKeyName: string
  createdAt: number
  referenceImage?: {
    name: string
    mimeType: string
    blob: Blob
  }
  images: Array<{
    url: string
    mimeType: string
    blob?: Blob
  }>
}

export interface ImageGenerationSession {
  id: string
  title: string
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
