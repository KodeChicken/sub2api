import { describe, expect, it } from 'vitest'
import { imageResultURLs, isLikelyImageModel } from './api'

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
})
