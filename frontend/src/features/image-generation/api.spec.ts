import { afterEach, describe, expect, it, vi } from 'vitest'
import { imageResultURLs, isLikelyImageModel, submitImageGeneration } from './api'

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
  })
})
