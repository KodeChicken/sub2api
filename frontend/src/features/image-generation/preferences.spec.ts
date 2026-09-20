import { beforeEach, describe, expect, it } from 'vitest'
import {
  imageSizeValues,
  isGptImageV2Model,
  loadImageGenerationPreferences,
  saveImageGenerationPreferences,
} from './preferences'

describe('image generation preferences', () => {
  beforeEach(() => localStorage.clear())

  it('enables 4K sizes for the gpt-image-2 model family only', () => {
    expect(isGptImageV2Model('gpt-image-2')).toBe(true)
    expect(isGptImageV2Model('gpt-image-2.5-flare')).toBe(true)
    expect(isGptImageV2Model('gpt-image-2-sunburst')).toBe(true)
    expect(isGptImageV2Model('gpt-image-20')).toBe(false)
    expect(imageSizeValues('gpt-image-2.5-flare')).toContain('3840x2160')
    expect(imageSizeValues('gpt-image-1.5')).not.toContain('3840x2160')
  })

  it('stores the selected model and parameters', () => {
    saveImageGenerationPreferences({
      selectedModelByKey: { 1: 'gpt-image-2.5-flare' },
      parametersByKeyModel: {
        '1:gpt-image-2.5-flare': { size: '3840x2160', quality: 'high', outputCount: 2 },
      },
    })

    expect(loadImageGenerationPreferences()).toEqual({
      selectedModelByKey: { 1: 'gpt-image-2.5-flare' },
      parametersByKeyModel: {
        '1:gpt-image-2.5-flare': { size: '3840x2160', quality: 'high', outputCount: 2 },
      },
    })
  })
})
