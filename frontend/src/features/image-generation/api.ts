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
	referenceImages?: File[]
	mask?: File
	parameters?: Record<string, string | number | boolean>
}

export type ImageGenerationSubmission =
  | { mode: 'async'; task: ImageGenerationTask }
  | { mode: 'sync'; result: ImageGenerationResult }

export interface ImageGenerationStreamEvent {
	type: string
	url?: string
	index?: number
}

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
  const basePath = input.referenceImages?.length ? '/v1/images/edits' : '/v1/images/generations'
  const path = useAsync ? `${basePath}/async` : basePath
  let body: BodyInit
  let headers: HeadersInit

  if (input.referenceImages?.length) {
    const form = new FormData()
    form.append('model', input.model)
    form.append('prompt', input.prompt)
    form.append('size', input.size)
    form.append('quality', input.quality)
    form.append('n', String(input.n))
		for (const image of input.referenceImages) form.append('image[]', image)
		if (input.mask) form.append('mask', input.mask)
		appendExtraParameters(form, input.parameters)
    body = form
    headers = authHeaders(apiKey)
  } else {
		body = JSON.stringify({
      model: input.model,
      prompt: input.prompt,
      size: input.size,
      quality: input.quality,
      n: input.n,
			...input.parameters,
    })
    headers = authHeaders(apiKey, { 'Content-Type': 'application/json' })
  }

  return { path, body, headers }
}

function appendExtraParameters(form: FormData, parameters?: Record<string, string | number | boolean>) {
	if (!parameters) return
	for (const [key, value] of Object.entries(parameters)) {
		if (['model', 'prompt', 'size', 'quality', 'n'].includes(key)) continue
		form.append(key, String(value))
	}
}

async function sendImageGenerationRequest(
  apiKey: string,
  input: SubmitImageGenerationInput,
  useAsync: boolean,
  signal?: AbortSignal,
): Promise<Response> {
  const request = buildImageGenerationRequest(apiKey, input, useAsync)
  const response = await fetch(buildGatewayUrl(request.path), {
    method: 'POST',
    headers: request.headers,
    body: request.body,
    signal,
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
  signal?: AbortSignal,
): Promise<ImageGenerationSubmission> {
  try {
    const response = await sendImageGenerationRequest(apiKey, input, true, signal)
    return { mode: 'async', task: await response.json() }
  } catch (error) {
    if (!isAsyncImageTasksDisabled(error)) throw error
  }

  const response = await sendImageGenerationRequest(apiKey, input, false, signal)
  return { mode: 'sync', result: await response.json() }
}

export async function streamImageGeneration(
	apiKey: string,
	input: SubmitImageGenerationInput,
	onEvent: (event: ImageGenerationStreamEvent) => void,
	signal?: AbortSignal,
): Promise<ImageGenerationResult> {
	const request = buildImageGenerationRequest(apiKey, {
		...input,
		parameters: { ...input.parameters, stream: true },
	}, false)
	const headers = new Headers(request.headers)
	headers.set('Accept', 'text/event-stream')
	const response = await fetch(buildGatewayUrl(request.path), {
		method: 'POST',
		headers,
		body: request.body,
		signal,
	})
	if (!response.ok) throw await parseGatewayError(response)
	if (!response.body || !response.headers.get('content-type')?.includes('text/event-stream')) {
		return response.json()
	}
	const reader = response.body.getReader()
	const decoder = new TextDecoder()
	let buffer = ''
	const completed: NonNullable<ImageGenerationResult['data']> = []
	while (true) {
		const { done, value } = await reader.read()
		buffer += decoder.decode(value || new Uint8Array(), { stream: !done })
		const frames = buffer.split(/\r?\n\r?\n/)
		buffer = frames.pop() || ''
		for (const frame of frames) parseImageStreamFrame(frame, completed, onEvent)
		if (done) break
	}
	if (buffer.trim()) parseImageStreamFrame(buffer, completed, onEvent)
	return { created: Math.floor(Date.now() / 1000), data: completed }
}

function parseImageStreamFrame(
	frame: string,
	completed: NonNullable<ImageGenerationResult['data']>,
	onEvent: (event: ImageGenerationStreamEvent) => void,
) {
	const data = frame.split(/\r?\n/)
		.filter(line => line.startsWith('data:'))
		.map(line => line.slice(5).trimStart())
		.join('\n')
	if (!data || data === '[DONE]') return
	const payload = JSON.parse(data)
	const type = String(payload.type || '')
	if (type === 'error' || payload.error) throw new Error(payload.error?.message || 'Image generation failed')
	const image = payload.url || (payload.b64_json ? `data:image/${payload.output_format || 'png'};base64,${payload.b64_json}` : '')
	if (type.endsWith('.partial_image') && image) {
		onEvent({ type, url: image, index: Number(payload.partial_image_index || 0) })
	}
	if ((type.endsWith('.completed') || type === 'image.completed') && image) {
		completed.push({ url: payload.url, b64_json: payload.b64_json, revised_prompt: payload.revised_prompt })
		onEvent({ type, url: image, index: completed.length - 1 })
	}
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

export async function cancelImageGenerationTask(apiKey: string, taskId: string): Promise<ImageGenerationTask> {
	const response = await fetch(buildGatewayUrl(`/v1/images/tasks/${encodeURIComponent(taskId)}/cancel`), {
		method: 'POST',
		headers: authHeaders(apiKey),
	})
	if (!response.ok) throw await parseGatewayError(response)
	return response.json()
}

export function imageResultURLs(result?: ImageGenerationResult): string[] {
  return (result?.data || [])
    .map((item) => {
      if (item.url) return item.url.startsWith('/') ? buildGatewayUrl(item.url) : item.url
      if (item.b64_json) return `data:image/png;base64,${item.b64_json}`
      return ''
    })
    .filter(Boolean)
}

export function isLikelyImageModel(model: ImageGenerationModel): boolean {
  return /(image|imagen|dall-e|grok.*imagine|nano.*banana)/i.test(model.id)
}
