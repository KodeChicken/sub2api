import { describe, expect, it } from 'vitest'
import { centerCrop, fitCropToRatio, resizeCropWithRatio } from './editor'

describe('image editor crop geometry', () => {
  it('fits common aspect ratios inside the source image', () => {
    expect(fitCropToRatio(4096, 4096, 16, 9)).toEqual({ x: 0, y: 896, width: 4096, height: 2304 })
    expect(fitCropToRatio(4096, 4096, 9, 16)).toEqual({ x: 896, y: 0, width: 2304, height: 4096 })
  })

  it('resizes a crop while preserving an explicit ratio', () => {
    const crop = { x: 10, y: 20, width: 800, height: 600 }
    expect(resizeCropWithRatio(crop, 'width', 960, 16 / 9)).toEqual({ x: 10, y: 20, width: 960, height: 540 })
    expect(resizeCropWithRatio(crop, 'height', 720, 16 / 9)).toEqual({ x: 10, y: 20, width: 1280, height: 720 })
  })

  it('centers a crop without changing its dimensions', () => {
    expect(centerCrop({ x: 0, y: 0, width: 261, height: 512 }, 4096, 4096))
      .toEqual({ x: 1918, y: 1792, width: 261, height: 512 })
  })
})
