import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ImageGenerationView from './ImageGenerationView.vue'

const mocks = vi.hoisted(() => ({
  showError: vi.fn(),
  loadKeys: vi.fn().mockResolvedValue({ items: [] }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

vi.mock('@/api', () => ({
  keysAPI: { list: mocks.loadKeys },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError: mocks.showError, showSuccess: vi.fn() }),
}))

vi.mock('./api', () => ({
  getImageGenerationTask: vi.fn(),
  imageResultURLs: vi.fn(),
  isLikelyImageModel: vi.fn(),
  listImageGenerationModels: vi.fn(),
  submitImageGeneration: vi.fn(),
}))

vi.mock('./history', () => ({
  cacheGeneratedImages: vi.fn(),
  createImageSession: vi.fn(() => ({ id: 'session-1', title: 'Session', createdAt: 1, updatedAt: 1 })),
  displayImageURL: vi.fn(),
  listImageHistory: vi.fn().mockResolvedValue([]),
  loadImageSessions: vi.fn(() => []),
  saveImageHistory: vi.fn(),
  saveImageSessions: vi.fn(),
}))

function clipboardEvent(items: Array<{ kind: string; type: string; getAsFile: () => File | null }>) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', {
    value: { files: [], items },
  })
  return event
}

describe('ImageGenerationView clipboard images', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.stubGlobal('URL', {
      createObjectURL: vi.fn(() => 'blob:reference-image'),
      revokeObjectURL: vi.fn(),
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('attaches an image pasted into the prompt form', async () => {
    const wrapper = mount(ImageGenerationView, {
      global: { stubs: { Icon: true, RouterLink: true } },
    })
    await flushPromises()
    const file = new File(['image'], 'clipboard.png', { type: 'image/png' })
    const event = clipboardEvent([{ kind: 'file', type: 'image/png', getAsFile: () => file }])

    wrapper.find('form').element.dispatchEvent(event)
    await wrapper.vm.$nextTick()

    expect(event.defaultPrevented).toBe(true)
    expect(wrapper.text()).toContain('clipboard.png')
    expect(URL.createObjectURL).toHaveBeenCalledWith(file)
  })

  it('does not intercept normal text paste', async () => {
    const wrapper = mount(ImageGenerationView, {
      global: { stubs: { Icon: true, RouterLink: true } },
    })
    await flushPromises()
    const event = clipboardEvent([{ kind: 'string', type: 'text/plain', getAsFile: () => null }])

    wrapper.find('form').element.dispatchEvent(event)
    await wrapper.vm.$nextTick()

    expect(event.defaultPrevented).toBe(false)
    expect(URL.createObjectURL).not.toHaveBeenCalled()
  })
})
