import type {
  ImageGenerationHistoryRecord,
	ImageGenerationReferenceImage,
  ImageGenerationSession,
  ImageGenerationSessionDraft,
} from './types'
import { apiClient } from '@/api/client'

const DATABASE_NAME = 'sub2api-image-generation'
const DATABASE_VERSION = 2
const HISTORY_STORE = 'history'
const DRAFT_STORE = 'drafts'
const SESSION_STORAGE_KEY = 'sub2api-image-generation-sessions-v1'

function openDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DATABASE_NAME, DATABASE_VERSION)
    request.onupgradeneeded = () => {
      const db = request.result
      if (!db.objectStoreNames.contains(HISTORY_STORE)) {
        const store = db.createObjectStore(HISTORY_STORE, { keyPath: 'id' })
        store.createIndex('createdAt', 'createdAt')
        store.createIndex('sessionId', 'sessionId')
      }
		if (!db.objectStoreNames.contains(DRAFT_STORE)) {
			db.createObjectStore(DRAFT_STORE, { keyPath: 'sessionId' })
		}
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error || new Error('Failed to open image history'))
  })
}

async function withStore<T>(
	storeName: string,
  mode: IDBTransactionMode,
  action: (store: IDBObjectStore) => IDBRequest<T>,
): Promise<T> {
  const db = await openDatabase()
  return new Promise((resolve, reject) => {
    const transaction = db.transaction(storeName, mode)
    const request = action(transaction.objectStore(storeName))
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error || new Error('Image history operation failed'))
    transaction.oncomplete = () => db.close()
    transaction.onerror = () => reject(transaction.error || new Error('Image history transaction failed'))
  })
}

export async function listLocalImageHistory(): Promise<ImageGenerationHistoryRecord[]> {
  const items = await withStore<ImageGenerationHistoryRecord[]>(HISTORY_STORE, 'readonly', (store) => store.getAll())
  return items.sort((a, b) => b.createdAt - a.createdAt)
}

export async function getImageHistory(id: string): Promise<ImageGenerationHistoryRecord | undefined> {
	try {
		const { data } = await apiClient.get<ImageGenerationHistoryRecord>(`/image-sessions/records/${encodeURIComponent(id)}`)
		return hydrateRecord(data)
	} catch (error) {
		if ((error as { status?: number }).status === 404) return undefined
		throw error
	}
}

export async function saveImageHistory(record: ImageGenerationHistoryRecord): Promise<void> {
	const payload = {
		...record,
		images: await Promise.all(record.images.map(image => storeImage(record.sessionId, image))),
		referenceImages: await Promise.all((record.referenceImages || []).map(image => storeReference(record.sessionId, image))),
		maskImage: record.maskImage ? await storeReference(record.sessionId, record.maskImage) : undefined,
		referenceImage: record.referenceImage ? await storeLegacyReference(record.sessionId, record.referenceImage) : undefined,
	}
	await apiClient.put(`/image-sessions/records/${encodeURIComponent(record.id)}`, payload)
}

export async function deleteImageHistory(id: string): Promise<void> {
	await apiClient.delete(`/image-sessions/records/${encodeURIComponent(id)}`)
}

export async function listImageHistory(): Promise<ImageGenerationHistoryRecord[]> {
	const { data } = await apiClient.get<ImageGenerationHistoryRecord[]>('/image-sessions/records')
	return Promise.all(data.map(hydrateRecord))
}

const uploadedBlobs = new WeakMap<Blob, string>()

async function uploadImage(sessionId: string, blob: Blob): Promise<string> {
	const existing = uploadedBlobs.get(blob)
	if (existing) return existing
	const form = new FormData()
	form.append('file', blob, 'image')
	const { data } = await apiClient.post<{ url: string }>(`/image-sessions/${encodeURIComponent(sessionId)}/assets`, form, {
		headers: { 'Content-Type': 'multipart/form-data' },
	})
	uploadedBlobs.set(blob, data.url)
	return data.url
}

async function storeImage(sessionId: string, image: ImageGenerationHistoryRecord['images'][number]) {
	if (image.url.startsWith('/api/v1/image-sessions/assets/') && image.blob && uploadedBlobs.get(image.blob) === image.url) {
		const { blob: _blob, ...metadata } = image
		return metadata
	}
	const blob = image.blob || await fetch(image.url).then(response => {
		if (!response.ok) throw new Error(`Cannot save generated image: HTTP ${response.status}`)
		return response.blob()
	})
	if (!blob) throw new Error('Cannot save generated image without file data')
	const url = await uploadImage(sessionId, blob)
	const { blob: _blob, ...metadata } = image
	return { ...metadata, url }
}

async function storeReference(sessionId: string, image: ImageGenerationReferenceImage) {
	const { blob, ...metadata } = image
	return { ...metadata, assetUrl: await uploadImage(sessionId, blob) }
}

async function storeLegacyReference(sessionId: string, image: NonNullable<ImageGenerationHistoryRecord['referenceImage']>) {
	return { name: image.name, mimeType: image.mimeType, assetUrl: await uploadImage(sessionId, image.blob) }
}

async function readAsset(url: string): Promise<Blob> {
	const path = new URL(url, window.location.origin).pathname
	const { data } = await apiClient.get<Blob>(path.replace(/^\/api\/v1/, ''), { responseType: 'blob' })
	uploadedBlobs.set(data, url)
	return data
}

async function hydrateReference(image: ImageGenerationReferenceImage): Promise<ImageGenerationReferenceImage> {
	return { ...image, blob: await readAsset(image.assetUrl || '') }
}

async function hydrateRecord(record: ImageGenerationHistoryRecord): Promise<ImageGenerationHistoryRecord> {
	const imageResults = await Promise.allSettled(record.images.map(image => readAsset(image.url)))
	const referenceResults = await Promise.allSettled((record.referenceImages || []).map(hydrateReference))
	const maskResult = record.maskImage ? await Promise.allSettled([hydrateReference(record.maskImage)]) : []
	const legacyResult = record.referenceImage ? await Promise.allSettled([readAsset(record.referenceImage.assetUrl || '')]) : []
	return {
		...record,
		images: record.images.map((image, index) => imageResults[index].status === 'fulfilled' ? { ...image, blob: imageResults[index].value } : image),
		referenceImages: referenceResults.filter(result => result.status === 'fulfilled').map(result => result.value),
		maskImage: maskResult[0]?.status === 'fulfilled' ? maskResult[0].value : undefined,
		referenceImage: record.referenceImage && legacyResult[0]?.status === 'fulfilled' ? { ...record.referenceImage, blob: legacyResult[0].value } : undefined,
	}
}

export async function cacheGeneratedImages(
	urls: string[],
	revisedPrompts: Array<string | undefined> = [],
): Promise<ImageGenerationHistoryRecord['images']> {
  return Promise.all(urls.map(async (url, index) => {
    try {
      const response = await fetch(url)
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      const blob = await response.blob()
		const dimensions = await imageDimensions(blob)
      return {
			url,
			mimeType: blob.type || 'image/png',
			blob,
			fileSizeBytes: blob.size,
			revisedPrompt: revisedPrompts[index],
			...dimensions,
		}
    } catch {
		return { url, mimeType: 'image/png', revisedPrompt: revisedPrompts[index] }
    }
  }))
}

async function imageDimensions(blob: Blob): Promise<{ width?: number; height?: number }> {
	if (typeof createImageBitmap !== 'function') return {}
	try {
		const bitmap = await createImageBitmap(blob)
		const dimensions = { width: bitmap.width, height: bitmap.height }
		bitmap.close()
		return dimensions
	} catch {
		return {}
	}
}

export function displayImageURL(image: ImageGenerationHistoryRecord['images'][number]): string {
  return image.blob ? URL.createObjectURL(image.blob) : image.url
}

export function loadLocalImageSessions(): ImageGenerationSession[] {
  try {
    const raw = localStorage.getItem(SESSION_STORAGE_KEY)
    const sessions = raw ? JSON.parse(raw) : []
    return Array.isArray(sessions) ? sessions : []
  } catch {
    return []
  }
}

export async function loadImageSessions(): Promise<ImageGenerationSession[]> {
	const { data } = await apiClient.get<ImageGenerationSession[]>('/image-sessions')
	return data
}

export async function saveImageSessions(sessions: ImageGenerationSession[]): Promise<void> {
	await Promise.all(sessions.map(session => apiClient.put('/image-sessions', session)))
}

export async function deleteImageSession(sessionId: string): Promise<void> {
	await apiClient.delete(`/image-sessions/${encodeURIComponent(sessionId)}`)
}

export function createImageSession(title = ''): ImageGenerationSession {
  const now = Date.now()
  return {
    id: crypto.randomUUID(),
    title: title.trim() || new Date(now).toLocaleString(),
    createdAt: now,
    updatedAt: now,
		sortOrder: now,
  }
}

export async function loadImageSessionDraft(sessionId: string): Promise<ImageGenerationSessionDraft | undefined> {
	const { data } = await apiClient.get<ImageGenerationSessionDraft | null>(`/image-sessions/${encodeURIComponent(sessionId)}/draft`)
	if (!data) return undefined
	return { ...data, referenceImages: await Promise.all((data.referenceImages || []).map(hydrateReference)), maskImage: data.maskImage ? await hydrateReference(data.maskImage) : undefined }
}

export async function saveImageSessionDraft(draft: ImageGenerationSessionDraft): Promise<void> {
	const payload = { ...draft, referenceImages: await Promise.all(draft.referenceImages.map(image => storeReference(draft.sessionId, image))), maskImage: draft.maskImage ? await storeReference(draft.sessionId, draft.maskImage) : undefined }
	await apiClient.put(`/image-sessions/${encodeURIComponent(draft.sessionId)}/draft`, payload)
}

export async function deleteImageSessionDraft(sessionId: string): Promise<void> {
	await apiClient.delete(`/image-sessions/${encodeURIComponent(sessionId)}/draft`)
}

export async function loadLocalImageSessionDraft(sessionId: string): Promise<ImageGenerationSessionDraft | undefined> {
	return withStore<ImageGenerationSessionDraft | undefined>(DRAFT_STORE, 'readonly', store => store.get(sessionId))
}
