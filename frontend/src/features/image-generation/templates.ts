import type { ImagePromptTemplate } from './types'

const STORAGE_KEY = 'sub2api-image-prompt-templates-v1'

export function loadImagePromptTemplates(): ImagePromptTemplate[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]')
    return Array.isArray(parsed) ? parsed : []
  } catch {
    return []
  }
}

export function saveImagePromptTemplates(templates: ImagePromptTemplate[]): void {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(templates))
}

export function createImagePromptTemplate(title: string, prompt: string, description = ''): ImagePromptTemplate {
  const now = Date.now()
  return {
    id: crypto.randomUUID(),
    title: title.trim(),
    prompt: prompt.trim(),
    description: description.trim(),
    createdAt: now,
    updatedAt: now,
  }
}
