import type {
  ImageGenerationHistoryRecord,
  ImageGenerationSession,
  ImageGenerationSessionDraft,
} from './types'

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

export async function listImageHistory(): Promise<ImageGenerationHistoryRecord[]> {
  const items = await withStore<ImageGenerationHistoryRecord[]>(HISTORY_STORE, 'readonly', (store) => store.getAll())
  return items.sort((a, b) => b.createdAt - a.createdAt)
}

export async function getImageHistory(id: string): Promise<ImageGenerationHistoryRecord | undefined> {
	return withStore<ImageGenerationHistoryRecord | undefined>(HISTORY_STORE, 'readonly', store => store.get(id))
}

export async function saveImageHistory(record: ImageGenerationHistoryRecord): Promise<void> {
  await withStore<IDBValidKey>(HISTORY_STORE, 'readwrite', (store) => store.put(record))
}

export async function deleteImageHistory(id: string): Promise<void> {
  await withStore<undefined>(HISTORY_STORE, 'readwrite', (store) => store.delete(id) as IDBRequest<undefined>)
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

export function loadImageSessions(): ImageGenerationSession[] {
  try {
    const raw = localStorage.getItem(SESSION_STORAGE_KEY)
    const sessions = raw ? JSON.parse(raw) : []
    return Array.isArray(sessions) ? sessions : []
  } catch {
    return []
  }
}

export function saveImageSessions(sessions: ImageGenerationSession[]): void {
  localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify(sessions))
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
	return withStore<ImageGenerationSessionDraft | undefined>(DRAFT_STORE, 'readonly', store => store.get(sessionId))
}

export async function saveImageSessionDraft(draft: ImageGenerationSessionDraft): Promise<void> {
	await withStore<IDBValidKey>(DRAFT_STORE, 'readwrite', store => store.put(draft))
}

export async function deleteImageSessionDraft(sessionId: string): Promise<void> {
	await withStore<undefined>(DRAFT_STORE, 'readwrite', store => store.delete(sessionId) as IDBRequest<undefined>)
}
