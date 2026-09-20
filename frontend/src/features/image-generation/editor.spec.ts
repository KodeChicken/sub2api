import { describe, expect, it } from 'vitest'
import { createEditorDocument, cropToAspectRatio, fitEditorImage } from './editor'

describe('local image editor geometry', () => {
  it('creates centered crop rectangles for common aspect ratios', () => {
    expect(cropToAspectRatio(1600, 1200, 1, 1)).toEqual({ x: 200, y: 0, width: 1200, height: 1200 })
    expect(cropToAspectRatio(1200, 1600, 16, 9)).toEqual({ x: 0, y: 463, width: 1200, height: 675 })
  })

  it('fits and covers the current crop without changing the source crop', () => {
    const document = createEditorDocument(1600, 1200)
    document.canvas.width = 800
    document.canvas.height = 800

    const contained = fitEditorImage(document, 'contain')
    const covered = fitEditorImage(document, 'cover')

    expect(contained.image.scaleX).toBe(0.5)
    expect(contained.image.y).toBe(100)
    expect(covered.image.scaleX).toBeCloseTo(2 / 3)
    expect(covered.image.x).toBeCloseTo(-133.333, 2)
    expect(contained.image.crop).toEqual(document.image.crop)
  })
})
