export interface LocalImageEditorDocument {
  canvas: {
    width: number
    height: number
    background:
      | { type: 'transparent' }
      | { type: 'color'; color: string }
      | { type: 'blurred-image'; blurRadius: number }
  }
  image: {
    x: number
    y: number
    scaleX: number
    scaleY: number
    rotation: number
    flipX: boolean
    flipY: boolean
    crop: { x: number; y: number; width: number; height: number }
  }
}

export function createEditorDocument(width: number, height: number): LocalImageEditorDocument {
  return {
    canvas: { width, height, background: { type: 'transparent' } },
    image: {
      x: 0,
      y: 0,
      scaleX: 1,
      scaleY: 1,
      rotation: 0,
      flipX: false,
      flipY: false,
      crop: { x: 0, y: 0, width, height },
    },
  }
}

export function cloneEditorDocument(document: LocalImageEditorDocument): LocalImageEditorDocument {
  return JSON.parse(JSON.stringify(document)) as LocalImageEditorDocument
}

export function fitEditorImage(document: LocalImageEditorDocument, mode: 'contain' | 'cover') {
  const next = cloneEditorDocument(document)
  const crop = next.image.crop
  const scale = mode === 'cover'
    ? Math.max(next.canvas.width / crop.width, next.canvas.height / crop.height)
    : Math.min(next.canvas.width / crop.width, next.canvas.height / crop.height)
  next.image.scaleX = scale
  next.image.scaleY = scale
  next.image.rotation = 0
  next.image.x = (next.canvas.width - crop.width * scale) / 2
  next.image.y = (next.canvas.height - crop.height * scale) / 2
  return next
}

export async function exportEditorImage(
  source: Blob,
  document: LocalImageEditorDocument,
  type = 'image/png',
  quality = 0.92,
): Promise<Blob> {
  const bitmap = await createImageBitmap(source)
  const canvas = window.document.createElement('canvas')
  canvas.width = document.canvas.width
  canvas.height = document.canvas.height
  const context = canvas.getContext('2d')
  if (!context) throw new Error('Canvas is unavailable')
  drawEditorBackground(context, bitmap, document)
  const image = document.image
  const crop = image.crop
  context.save()
  context.translate(image.x, image.y)
  context.rotate(image.rotation * Math.PI / 180)
  context.scale(image.flipX ? -image.scaleX : image.scaleX, image.flipY ? -image.scaleY : image.scaleY)
  context.drawImage(bitmap, crop.x, crop.y, crop.width, crop.height, image.flipX ? -crop.width : 0, image.flipY ? -crop.height : 0, crop.width, crop.height)
  context.restore()
  bitmap.close()
  return new Promise((resolve, reject) => canvas.toBlob(blob => blob ? resolve(blob) : reject(new Error('Image export failed')), type, quality))
}

export async function exportOutpaintInputs(
  source: Blob,
  document: LocalImageEditorDocument,
): Promise<{ image: Blob; mask: Blob }> {
  const bitmap = await createImageBitmap(source)
  const imageCanvas = createCanvas(document.canvas.width, document.canvas.height)
  const maskCanvas = createCanvas(document.canvas.width, document.canvas.height)
  const imageContext = imageCanvas.getContext('2d')
  const maskContext = maskCanvas.getContext('2d')
  if (!imageContext || !maskContext) {
    bitmap.close()
    throw new Error('Canvas is unavailable')
  }
  drawEditorImage(imageContext, bitmap, document)
  drawEditorImage(maskContext, bitmap, document)
  maskContext.globalCompositeOperation = 'source-in'
  maskContext.fillStyle = '#ffffff'
  maskContext.fillRect(0, 0, maskCanvas.width, maskCanvas.height)
  bitmap.close()
  const [image, mask] = await Promise.all([
    canvasToBlob(imageCanvas, 'image/png', 1),
    canvasToBlob(maskCanvas, 'image/png', 1),
  ])
  return { image, mask }
}

export function cropToAspectRatio(
  sourceWidth: number,
  sourceHeight: number,
  ratioWidth: number,
  ratioHeight: number,
) {
  const ratio = ratioWidth / ratioHeight
  let width = sourceWidth
  let height = Math.round(width / ratio)
  if (height > sourceHeight) {
    height = sourceHeight
    width = Math.round(height * ratio)
  }
  return {
    x: Math.round((sourceWidth - width) / 2),
    y: Math.round((sourceHeight - height) / 2),
    width,
    height,
  }
}

function drawEditorBackground(
  context: CanvasRenderingContext2D,
  bitmap: ImageBitmap,
  document: LocalImageEditorDocument,
) {
  const background = document.canvas.background
  if (background.type === 'color') {
    context.fillStyle = background.color
    context.fillRect(0, 0, document.canvas.width, document.canvas.height)
    return
  }
  if (background.type !== 'blurred-image') return
  const scale = Math.max(document.canvas.width / bitmap.width, document.canvas.height / bitmap.height)
  const width = bitmap.width * scale
  const height = bitmap.height * scale
  context.save()
  context.filter = `blur(${background.blurRadius}px)`
  context.drawImage(bitmap, (document.canvas.width - width) / 2, (document.canvas.height - height) / 2, width, height)
  context.restore()
}

function drawEditorImage(
  context: CanvasRenderingContext2D,
  bitmap: ImageBitmap,
  document: LocalImageEditorDocument,
) {
  const image = document.image
  const crop = image.crop
  context.save()
  context.beginPath()
  context.rect(0, 0, document.canvas.width, document.canvas.height)
  context.clip()
  context.translate(image.x, image.y)
  context.rotate(image.rotation * Math.PI / 180)
  context.scale(image.flipX ? -image.scaleX : image.scaleX, image.flipY ? -image.scaleY : image.scaleY)
  context.drawImage(bitmap, crop.x, crop.y, crop.width, crop.height, image.flipX ? -crop.width : 0, image.flipY ? -crop.height : 0, crop.width, crop.height)
  context.restore()
}

function createCanvas(width: number, height: number) {
  const canvas = window.document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  return canvas
}

function canvasToBlob(canvas: HTMLCanvasElement, type: string, quality: number): Promise<Blob> {
  return new Promise((resolve, reject) => canvas.toBlob(blob => blob ? resolve(blob) : reject(new Error('Image export failed')), type, quality))
}
