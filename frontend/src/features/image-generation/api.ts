import { buildGatewayUrl } from '@/api/client'
import type { ImageGenerationModel, ImageGenerationResult, ImageGenerationTask } from './types'

interface ModelsResponse {
  data?: ImageGenerationModel[]
}

export interface SubmitImageGenerationInput {
  model: string
  prompt: string
  size: string
  quality: string
  n: number
  referenceImage?: File | null
}

export type ImageGenerationSubmission =
  | { mode: 'async'; task: ImageGenerationTask }
  | { mode: 'sync'; result: ImageGenerationResult }

async function parseGatewayError(response: Response): Promise<Error> {
  let message = response.statusText || `HTTP ${response.status}`
  let code: string | number = response.status
  try {
    const body = await response.json()
    message = body?.error?.message || body?.message || message
    code = body?.error?.code || body?.error?.type || body?.code || code
  } catch {
    // Keep the HTTP fallback for non-JSON gateway errors.
  }
  const error = new Error(message)
  ;(error as Error & { code?: string | number; status?: number }).code = code
  ;(error as Error & { code?: string | number; status?: number }).status = response.status
  return error
}

function authHeaders(apiKey: string, extra?: HeadersInit): HeadersInit {
  return {
    Authorization: `Bearer ${apiKey}`,
    ...extra,
  }
}

export async function listImageGenerationModels(apiKey: string): Promise<ImageGenerationModel[]> {
  const response = await fetch(buildGatewayUrl('/v1/models'), {
    headers: authHeaders(apiKey),
  })
  if (!response.ok) throw await parseGatewayError(response)
  const body = await response.json() as ModelsResponse
  return (body.data || []).filter((item) => typeof item?.id === 'string' && item.id.trim())
}

function buildImageGenerationRequest(apiKey: string, input: SubmitImageGenerationInput, useAsync: boolean) {
  const basePath = input.referenceImage ? '/v1/images/edits' : '/v1/images/generations'
  const path = useAsync ? `${basePath}/async` : basePath
  let body: BodyInit
  let headers: HeadersInit

  if (input.referenceImage) {
    const form = new FormData()
    form.append('model', input.model)
    form.append('prompt', input.prompt)
    form.append('size', input.size)
    form.append('quality', input.quality)
    form.append('n', String(input.n))
    form.append('image', input.referenceImage)
    body = form
    headers = authHeaders(apiKey)
  } else {
    body = JSON.stringify({
      model: input.model,
      prompt: input.prompt,
      size: input.size,
      quality: input.quality,
      n: input.n,
      response_format: 'url',
    })
    headers = authHeaders(apiKey, { 'Content-Type': 'application/json' })
  }

  return { path, body, headers }
}

async function sendImageGenerationRequest(
  apiKey: string,
  input: SubmitImageGenerationInput,
  useAsync: boolean,
): Promise<Response> {
  const request = buildImageGenerationRequest(apiKey, input, useAsync)
  const response = await fetch(buildGatewayUrl(request.path), {
    method: 'POST',
    headers: request.headers,
    body: request.body,
  })
  if (!response.ok) throw await parseGatewayError(response)
  return response
}

function isAsyncImageTasksDisabled(error: unknown): boolean {
  const gatewayError = error as Error & { status?: number }
  return gatewayError?.status === 404
    && gatewayError.message.toLowerCase().includes('async image tasks are not enabled')
}

export async function submitImageGeneration(
  apiKey: string,
  input: SubmitImageGenerationInput,
): Promise<ImageGenerationSubmission> {
  try {
    const response = await sendImageGenerationRequest(apiKey, input, true)
    return { mode: 'async', task: await response.json() }
  } catch (error) {
    if (!isAsyncImageTasksDisabled(error)) throw error
  }

  const response = await sendImageGenerationRequest(apiKey, input, false)
  return { mode: 'sync', result: await response.json() }
}

export async function getImageGenerationTask(
  apiKey: string,
  taskId: string,
  signal?: AbortSignal,
): Promise<ImageGenerationTask> {
  const response = await fetch(buildGatewayUrl(`/v1/images/tasks/${encodeURIComponent(taskId)}`), {
    headers: authHeaders(apiKey),
    signal,
  })
  if (!response.ok) throw await parseGatewayError(response)
  return response.json()
}

export function imageResultURLs(result?: ImageGenerationResult): string[] {
  return (result?.data || [])
    .map((item) => {
      if (item.url) return item.url
      if (item.b64_json) return `data:image/png;base64,${item.b64_json}`
      return ''
    })
    .filter(Boolean)
}

export function isLikelyImageModel(model: ImageGenerationModel): boolean {
  return /(image|imagen|dall-e|grok.*imagine|nano.*banana)/i.test(model.id)
}
