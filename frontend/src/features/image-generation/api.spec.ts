import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  cancelImageGenerationTask,
  imageResultURLs,
  isLikelyImageModel,
  streamImageGeneration,
  submitImageGeneration,
} from './api'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('image generation API helpers', () => {
  it('extracts object URLs and base64 image payloads', () => {
    expect(imageResultURLs({
      data: [
        { url: 'https://cdn.example.com/result.png' },
        { b64_json: 'YWJj' },
        {},
      ],
    })).toEqual([
      'https://cdn.example.com/result.png',
      'data:image/png;base64,YWJj',
    ])
  })

  it('prioritizes common image model names without excluding custom models', () => {
    expect(isLikelyImageModel({ id: 'gpt-image-2' })).toBe(true)
    expect(isLikelyImageModel({ id: 'grok-imagine-1.0' })).toBe(true)
    expect(isLikelyImageModel({ id: 'custom-vision-model' })).toBe(false)
  })

  it('falls back to the synchronous endpoint when async image tasks are disabled', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        error: { message: 'async image tasks are not enabled' },
      }), { status: 404, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        data: [{ url: 'https://cdn.example.com/result.png' }],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    const submission = await submitImageGeneration('sk-test', {
      model: 'gpt-image-1',
      prompt: 'a lighthouse',
      size: '1024x1024',
      quality: 'auto',
      n: 1,
    })

    expect(submission).toEqual({
      mode: 'sync',
      result: { data: [{ url: 'https://cdn.example.com/result.png' }] },
    })
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(fetchMock.mock.calls[0][0]).toContain('/v1/images/generations/async')
    expect(fetchMock.mock.calls[1][0]).toContain('/v1/images/generations')
    expect(fetchMock.mock.calls[1][0]).not.toContain('/async')
    const body = JSON.parse(String(fetchMock.mock.calls[1][1]?.body))
    expect(body).not.toHaveProperty('response_format')
  })

  it('submits multiple references and an outpaint mask in upload order', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      id: 'task-1', status: 'processing',
    }), { status: 202, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const first = new File(['first'], 'first.png', { type: 'image/png' })
    const second = new File(['second'], 'second.webp', { type: 'image/webp' })
    const mask = new File(['mask'], 'mask.png', { type: 'image/png' })

    await submitImageGeneration('sk-test', {
      model: 'gpt-image-2',
      prompt: 'extend the scene',
      size: '2048x1152',
      quality: 'high',
      n: 1,
      referenceImages: [first, second],
      mask,
      parameters: { input_fidelity: 'high' },
    })

    const body = fetchMock.mock.calls[0][1]?.body as FormData
    expect(fetchMock.mock.calls[0][0]).toContain('/v1/images/edits/async')
    expect(body.getAll('image[]')).toEqual([first, second])
    expect(body.get('mask')).toBe(mask)
    expect(body.get('input_fidelity')).toBe('high')
  })

  it('parses partial and completed SSE image frames', async () => {
    const payload = [
      'data: {"type":"image_generation.partial_image","b64_json":"cGFydGlhbA==","output_format":"png"}\n\n',
      'data: {"type":"image_generation.completed","b64_json":"ZmluYWw=","revised_prompt":"revised"}\n\n',
      'data: [DONE]\n\n',
    ]
    const stream = new ReadableStream({
      start(controller) {
        payload.forEach(frame => controller.enqueue(new TextEncoder().encode(frame)))
        controller.close()
      },
    })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(stream, {
      status: 200,
      headers: { 'Content-Type': 'text/event-stream' },
    })))
    const events: Array<{ type: string; url?: string }> = []

    const result = await streamImageGeneration('sk-test', {
      model: 'gpt-image-2', prompt: 'test', size: '1024x1024', quality: 'auto', n: 1,
    }, event => events.push(event))

    expect(events).toEqual([
      { type: 'image_generation.partial_image', url: 'data:image/png;base64,cGFydGlhbA==', index: 0 },
      { type: 'image_generation.completed', url: 'data:image/png;base64,ZmluYWw=', index: 0 },
    ])
    expect(result.data).toEqual([{ url: undefined, b64_json: 'ZmluYWw=', revised_prompt: 'revised' }])
  })

  it('cancels an asynchronous image task', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      id: 'task-1', status: 'cancelled',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    await cancelImageGenerationTask('sk-test', 'task/1')

    expect(fetchMock.mock.calls[0][0]).toContain('/v1/images/tasks/task%2F1/cancel')
    expect(fetchMock.mock.calls[0][1]?.method).toBe('POST')
  })
})
