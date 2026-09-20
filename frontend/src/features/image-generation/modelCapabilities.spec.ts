import { describe, expect, it } from 'vitest'
import {
  imageModelCapabilities,
  imageSizeOptionsForAspectRatio,
  normalizeImageParameters,
  preferredImageSizeForAspectRatio,
  validateCustomImageSize,
} from './modelCapabilities'

describe('image model capabilities', () => {
  it('exposes 4K sizes for gpt-image-2 profiles and later 2.x variants', () => {
    for (const model of ['gpt-image-2', 'gpt-image-2.5-flare', 'gpt-image-2-pro']) {
      const sizes = imageModelCapabilities(model).parameters.find(item => item.key === 'size')?.options
      expect(sizes).toContainEqual({ value: '3840x2160', label: '3840 × 2160 · 4K' })
      expect(sizes).toContainEqual({ value: '2160x3840', label: '2160 × 3840 · 4K' })
      expect(imageModelCapabilities(model).parameters.find(item => item.key === 'size')?.customSize).toBeDefined()
    }
  })

  it('does not mistake unrelated gpt-image-20 names for the v2 profile', () => {
    const sizes = imageModelCapabilities('gpt-image-20').parameters.find(item => item.key === 'size')?.options
    expect(sizes).not.toContainEqual(expect.objectContaining({ value: '3840x2160' }))
  })

  it('drops edit-only settings until a reference image is present', () => {
    const values = { size: '1024x1024', quality: 'high', input_fidelity: 'high' }
    expect(normalizeImageParameters('gpt-image-1.5', values, false)).not.toHaveProperty('input_fidelity')
    expect(normalizeImageParameters('gpt-image-1.5', values, true)).toHaveProperty('input_fidelity', 'high')
  })

  it('accepts valid GPT Image 2 custom sizes and rejects invalid dimensions', () => {
    const size = imageModelCapabilities('gpt-image-2').parameters.find(item => item.key === 'size')!
    expect(validateCustomImageSize('1280x1024', size.customSize!)).toBe('')
    expect(normalizeImageParameters('gpt-image-2', { size: '1280x1024' }, false)).toHaveProperty('size', '1280x1024')
    expect(validateCustomImageSize('1279x1024', size.customSize!)).toContain('16')
    expect(normalizeImageParameters('gpt-image-2', { size: '3840x3840' }, false)).toHaveProperty('size', '1024x1024')
  })

  it('matches the GPT Image controls for moderation and image count', () => {
    const parameters = imageModelCapabilities('gpt-image-2').parameters
    expect(parameters.find(item => item.key === 'moderation')?.options).toEqual([
      { value: 'auto', label: 'auto' },
      { value: 'low', label: 'low' },
    ])
    expect(parameters.find(item => item.key === 'n')?.max).toBe(10)
  })

  it('maps aspect ratios to the closest compatible size tier', () => {
    const size = imageModelCapabilities('gpt-image-2').parameters.find(item => item.key === 'size')!
    expect(preferredImageSizeForAspectRatio(size, '16:9', '2160x3840')).toBe('3840x2160')
    expect(preferredImageSizeForAspectRatio(size, '9:16', '3840x2160')).toBe('2160x3840')
    expect(preferredImageSizeForAspectRatio(size, '16:9', '1024x1024')).toBe('1536x864')
    expect(imageSizeOptionsForAspectRatio(size, '16:9').map(option => option.value)).toEqual([
      'auto', '1536x864', '2048x1152', '3840x2160',
    ])
  })

  it('recognizes the approximate DALL-E landscape and portrait sizes', () => {
    const size = imageModelCapabilities('dall-e-3').parameters.find(item => item.key === 'size')!
    expect(preferredImageSizeForAspectRatio(size, '16:9', '1024x1792')).toBe('1792x1024')
    expect(preferredImageSizeForAspectRatio(size, '9:16', '1792x1024')).toBe('1024x1792')
  })
})
