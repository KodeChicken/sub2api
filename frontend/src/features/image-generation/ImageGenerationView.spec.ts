import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ImageGenerationView from './ImageGenerationView.vue'
import Select from '@/components/common/Select.vue'

const mocks = vi.hoisted(() => ({
  showError: vi.fn(),
  showSuccess: vi.fn(),
  loadKeys: vi.fn().mockResolvedValue({ items: [] }),
  listModels: vi.fn().mockResolvedValue([]),
  submitGeneration: vi.fn(),
	submitAsyncGeneration: vi.fn(),
	getTask: vi.fn(),
	getHistory: vi.fn(),
  streamGeneration: vi.fn(),
  cancelGeneration: vi.fn(),
  cacheImages: vi.fn(),
  listHistory: vi.fn().mockResolvedValue([]),
  saveHistory: vi.fn(),
  loadSessions: vi.fn(() => []),
  saveSessions: vi.fn(),
  deleteHistory: vi.fn(),
	deleteSession: vi.fn(),
  loadDraft: vi.fn(),
  saveDraft: vi.fn(),
  deleteDraft: vi.fn(),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, string>) => params?.name ? `${key}:${params.name}` : key,
  }),
}))

vi.mock('@/api', () => ({
  keysAPI: { list: mocks.loadKeys },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess }),
  useAuthStore: () => ({ isAdmin: false }),
}))

vi.mock('./api', () => ({
  getImageGenerationTask: mocks.getTask,
  cancelImageGenerationTask: mocks.cancelGeneration,
  imageResultURLs: (result?: { data?: Array<{ url?: string }> }) => result?.data?.map((item) => item.url || '').filter(Boolean) || [],
  isLikelyImageModel: (model: { id: string }) => model.id.includes('image'),
  listImageGenerationModels: mocks.listModels,
  submitImageGeneration: mocks.submitGeneration,
	submitAsyncImageGeneration: mocks.submitAsyncGeneration,
	isAsyncImageTasksDisabled: (error: { status?: number }) => error?.status === 404,
  streamImageGeneration: mocks.streamGeneration,
}))

vi.mock('./history', () => ({
  cacheGeneratedImages: mocks.cacheImages,
  createImageSession: vi.fn((title: string) => ({ id: 'session-1', title, createdAt: 1, updatedAt: 1 })),
  deleteImageHistory: mocks.deleteHistory,
	deleteImageSession: mocks.deleteSession,
  deleteImageSessionDraft: mocks.deleteDraft,
  displayImageURL: vi.fn((image: { url: string }) => image.url),
  listImageHistory: mocks.listHistory,
	getImageHistory: mocks.getHistory,
  loadImageSessions: mocks.loadSessions,
	loadLocalImageSessions: vi.fn(() => []),
	listLocalImageHistory: vi.fn().mockResolvedValue([]),
	loadLocalImageSessionDraft: vi.fn(),
  loadImageSessionDraft: mocks.loadDraft,
  saveImageHistory: mocks.saveHistory,
  saveImageSessionDraft: mocks.saveDraft,
  saveImageSessions: mocks.saveSessions,
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
    localStorage.clear()
    mocks.loadKeys.mockResolvedValue({ items: [] })
    mocks.listModels.mockResolvedValue([])
    mocks.loadSessions.mockReturnValue([])
    mocks.listHistory.mockResolvedValue([])
    mocks.cacheImages.mockResolvedValue([{ url: 'result.png', mimeType: 'image/png' }])
    mocks.loadDraft.mockResolvedValue(undefined)
    mocks.saveDraft.mockResolvedValue(undefined)
    mocks.deleteDraft.mockResolvedValue(undefined)
		mocks.deleteSession.mockResolvedValue(undefined)
    mocks.cancelGeneration.mockResolvedValue({ status: 'cancelled' })
		mocks.submitAsyncGeneration.mockRejectedValue({ status: 404 })
		mocks.getHistory.mockResolvedValue(undefined)
    mocks.streamGeneration.mockImplementation(async (key, input, _onEvent, signal) => {
      const submission = await mocks.submitGeneration(key, input, signal)
      return submission.mode === 'sync' ? submission.result : submission.task.result
    })
    vi.stubGlobal('URL', {
      createObjectURL: vi.fn(() => 'blob:reference-image'),
      revokeObjectURL: vi.fn(),
    })
  })

  afterEach(() => {
    vi.useRealTimers()
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

  it('saves a cloneable draft after restoring a mask image', async () => {
    vi.useFakeTimers()
    const mask = {
      id: 'mask-1',
      name: 'mask.png',
      mimeType: 'image/png',
      blob: new Blob(['mask'], { type: 'image/png' }),
    }
    mocks.loadDraft.mockResolvedValue({
      sessionId: 'session-1',
      prompt: 'Restore this draft',
      referenceImages: [],
      maskImage: mask,
      updatedAt: 1,
    })
    const wrapper = mount(ImageGenerationView, {
      global: { stubs: { Icon: true, RouterLink: true } },
    })
    await flushPromises()

    vi.advanceTimersByTime(300)
    await flushPromises()

    const savedDraft = mocks.saveDraft.mock.calls.at(-1)?.[0]
    expect(savedDraft?.maskImage).toEqual(mask)
    expect(() => structuredClone(savedDraft)).not.toThrow()
    wrapper.unmount()
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

  it('renames a session inline', async () => {
    const wrapper = mount(ImageGenerationView, {
      global: { stubs: { Icon: true, RouterLink: true } },
    })
    await flushPromises()

    await wrapper.get('[data-testid="rename-session"]').trigger('click')
    const input = wrapper.get<HTMLInputElement>('[data-testid="session-title-input"]')
    await input.setValue('  Product launch  ')
    await input.trigger('keydown', { key: 'Enter' })

    expect(wrapper.text()).toContain('Product launch')
		expect(mocks.saveSessions).toHaveBeenCalled()
  })

  it('moves the prompt and reference image into the message stream while generating', async () => {
    mocks.loadKeys.mockResolvedValue({
      items: [{
        id: 1,
        name: 'Image key',
        key: 'sk-test',
        status: 'active',
        group: { name: 'Default', platform: 'openai', allow_image_generation: true },
      }],
    })
    mocks.listModels.mockResolvedValue([{ id: 'gpt-image-1' }])
    let finishGeneration: ((value: { mode: 'sync'; result: { data: Array<{ url: string }> } }) => void) | undefined
    mocks.submitGeneration.mockReturnValue(new Promise((resolve) => {
      finishGeneration = resolve
    }))
    const wrapper = mount(ImageGenerationView, {
      global: { stubs: { Icon: true, RouterLink: true } },
    })
    await flushPromises()
    const file = new File(['image'], 'reference.png', { type: 'image/png' })
    wrapper.find('form').element.dispatchEvent(clipboardEvent([
      { kind: 'file', type: 'image/png', getAsFile: () => file },
    ]))
    await wrapper.get('textarea').setValue('生成一张夏日海边宣传海报')

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.get<HTMLTextAreaElement>('textarea').element.value).toBe('')
    expect(wrapper.get('article img').attributes('alt')).toContain('reference.png')
    expect(wrapper.get('[data-testid="session-title"]').text()).toBe('生成一张夏日海边宣传海报')
    expect(wrapper.get('[data-testid="user-message"]').classes()).toEqual(expect.arrayContaining([
      'w-fit', 'max-w-full', 'bg-gray-100', 'text-gray-900', 'dark:bg-dark-800', 'dark:text-gray-100',
    ]))
    expect(wrapper.findAll('[data-testid="user-message"]')).toHaveLength(1)

    finishGeneration?.({ mode: 'sync', result: { data: [{ url: 'result.png' }] } })
    await flushPromises()
    expect(mocks.showSuccess).toHaveBeenCalled()
    expect(wrapper.get('[data-testid="user-message"]').classes()).toEqual(expect.arrayContaining([
      'w-fit', 'max-w-full', 'bg-gray-100', 'text-gray-900', 'dark:bg-dark-800', 'dark:text-gray-100',
    ]))
    expect(mocks.saveHistory).toHaveBeenCalledWith(expect.objectContaining({
      prompt: '生成一张夏日海边宣传海报',
      referenceImages: [expect.objectContaining({ name: 'reference.png', blob: file })],
    }))
  })

  it('allows a new session during generation and keeps failure in the original session', async () => {
    mocks.loadKeys.mockResolvedValue({ items: [{ id: 1, name: 'Image key', key: 'sk-test', status: 'active', group: { name: 'Default', platform: 'openai', allow_image_generation: true } }] })
    mocks.listModels.mockResolvedValue([{ id: 'gpt-image-1' }])
    let failGeneration: ((error: Error) => void) | undefined
    mocks.submitGeneration.mockReturnValue(new Promise((_resolve, reject) => { failGeneration = reject }))
    const wrapper = mount(ImageGenerationView, { global: { stubs: { Icon: true, RouterLink: true } } })
    await flushPromises()
    await wrapper.get('textarea').setValue('Original prompt')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.findAll('[data-testid="user-message"]')).toHaveLength(1)

    const { createImageSession } = await import('./history')
    vi.mocked(createImageSession).mockReturnValueOnce({ id: 'session-2', title: 'New session', createdAt: 2, updatedAt: 2 })
    await wrapper.get('aside button[title="imageGeneration.sessions.new"]').trigger('click')
    await flushPromises()
    expect(mocks.saveSessions).toHaveBeenCalledWith([expect.objectContaining({ id: 'session-1', title: 'Original prompt' })])
    await wrapper.get('textarea').setValue('New session draft')
    failGeneration?.(new Error('upstream failed'))
    await flushPromises()
    expect(wrapper.get<HTMLTextAreaElement>('textarea').element.value).toBe('New session draft')
    expect(wrapper.findAll('[data-testid="user-message"]')).toHaveLength(0)
    expect(mocks.saveHistory).toHaveBeenCalledWith(expect.objectContaining({ sessionId: 'session-1', status: 'failed' }))
    wrapper.unmount()
  })

  it('sends with Enter and keeps Shift+Enter for line breaks', async () => {
    mocks.loadKeys.mockResolvedValue({
      items: [{
        id: 1,
        name: 'Image key',
        key: 'sk-test',
        status: 'active',
        group: { name: 'Default', platform: 'openai', allow_image_generation: true },
      }],
    })
    mocks.listModels.mockResolvedValue([{ id: 'gpt-image-1' }])
    mocks.submitGeneration.mockResolvedValue({ mode: 'sync', result: { data: [{ url: 'result.png' }] } })
    const wrapper = mount(ImageGenerationView, {
      global: { stubs: { Icon: true, RouterLink: true } },
    })
    await flushPromises()
    const textarea = wrapper.get('textarea')
    await textarea.setValue('Generate a poster')
    const lineBreak = new KeyboardEvent('keydown', { key: 'Enter', shiftKey: true, bubbles: true, cancelable: true })
    textarea.element.dispatchEvent(lineBreak)
    expect(lineBreak.defaultPrevented).toBe(false)

    const submit = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    textarea.element.dispatchEvent(submit)
    await flushPromises()

    expect(submit.defaultPrevented).toBe(true)
    expect(mocks.submitGeneration).toHaveBeenCalledTimes(1)
  })

  it('restores the prompt and reference image when generation is stopped', async () => {
    mocks.loadKeys.mockResolvedValue({
      items: [{
        id: 1,
        name: 'Image key',
        key: 'sk-test',
        status: 'active',
        group: { name: 'Default', platform: 'openai', allow_image_generation: true },
      }],
    })
    mocks.listModels.mockResolvedValue([{ id: 'gpt-image-1' }])
    mocks.submitGeneration.mockImplementation((_key, _input, signal: AbortSignal) => new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true })
    }))
    const wrapper = mount(ImageGenerationView, {
      global: { stubs: { Icon: true, RouterLink: true } },
    })
    await flushPromises()
    const file = new File(['image'], 'reference.png', { type: 'image/png' })
    wrapper.find('form').element.dispatchEvent(clipboardEvent([
      { kind: 'file', type: 'image/png', getAsFile: () => file },
    ]))
    await wrapper.get('textarea').setValue('Keep this draft')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    await wrapper.get('[data-testid="stop-generation"]').trigger('click')
    await flushPromises()

    expect(wrapper.get<HTMLTextAreaElement>('textarea').element.value).toBe('Keep this draft')
    expect(wrapper.get('form').text()).toContain('reference.png')
    expect(mocks.showError).not.toHaveBeenCalled()
  })

  it('deletes a session and selects the next one', async () => {
    mocks.loadSessions.mockReturnValue([
      { id: 'session-1', title: 'First session', createdAt: 1, updatedAt: 2 },
      { id: 'session-2', title: 'Second session', createdAt: 1, updatedAt: 1 },
    ])
    vi.stubGlobal('confirm', vi.fn(() => true))
    const wrapper = mount(ImageGenerationView, {
      global: { stubs: { Icon: true, RouterLink: true } },
    })
    await flushPromises()

    await wrapper.findAll('[data-testid="delete-session"]')[0].trigger('click')
    await flushPromises()

    expect(wrapper.text()).not.toContain('First session')
    expect(wrapper.get('[data-testid="session-title"]').text()).toBe('Second session')
		expect(mocks.deleteSession).toHaveBeenCalledWith('session-1')
  })

  it('restores model parameters and exposes 4K only for gpt-image-2 models', async () => {
    localStorage.setItem('image-generation-preferences-v1', JSON.stringify({
      selectedModelByKey: { 1: 'gpt-image-2.5-flare' },
      parametersByKeyModel: {
        '1:gpt-image-2.5-flare': { size: '3840x2160', quality: 'high', outputCount: 3 },
      },
    }))
    mocks.loadKeys.mockResolvedValue({
      items: [{
        id: 1,
        name: 'Image key',
        key: 'sk-test',
        status: 'active',
        group: { name: 'Default', platform: 'openai', allow_image_generation: true },
      }],
    })
    mocks.listModels.mockResolvedValue([
      { id: 'gpt-image-1.5' },
      { id: 'gpt-image-2.5-flare' },
    ])
    const wrapper = mount(ImageGenerationView, {
      global: { stubs: { Icon: true, RouterLink: true } },
    })
    await flushPromises()
    let selects = wrapper.findAllComponents(Select)

    expect(selects[1].props('modelValue')).toBe('gpt-image-2.5-flare')
    expect(selects[4].props('modelValue')).toBe('3840x2160')
    expect(selects[5].props('modelValue')).toBe('high')
    expect(selects[4].props('options')).toContainEqual({ value: '3840x2160', label: '3840 × 2160 · 4K' })
    expect(wrapper.findAll<HTMLInputElement>('input[type="number"]').at(-1)?.element.value).toBe('3')

    selects[1].vm.$emit('update:modelValue', 'gpt-image-1.5')
    await wrapper.vm.$nextTick()
    selects = wrapper.findAllComponents(Select)

    expect(selects[4].props('modelValue')).toBe('1024x1024')
    expect(selects[4].props('options')).not.toContainEqual(expect.objectContaining({ value: '3840x2160' }))
    expect(JSON.parse(localStorage.getItem('image-generation-preferences-v1') || '{}')).toMatchObject({
      selectedModelByKey: { 1: 'gpt-image-1.5' },
      parametersByKeyModel: {
        '1:gpt-image-1.5': { size: '1024x1024', quality: 'auto', outputCount: 1 },
      },
    })

    selects[1].vm.$emit('update:modelValue', 'gpt-image-2.5-flare')
    await wrapper.vm.$nextTick()
    selects = wrapper.findAllComponents(Select)

    expect(selects[4].props('modelValue')).toBe('3840x2160')
    expect(selects[5].props('modelValue')).toBe('high')
    expect(wrapper.findAll<HTMLInputElement>('input[type="number"]').at(-1)?.element.value).toBe('3')
  })

  it('updates the image size when the aspect ratio changes', async () => {
    localStorage.setItem('image-generation-preferences-v1', JSON.stringify({
      selectedModelByKey: { 1: 'gpt-image-2' },
      parametersByKeyModel: {
        '1:gpt-image-2': {
          size: '2160x3840',
          quality: 'high',
          outputCount: 1,
          values: { aspect_ratio: '9:16', size: '2160x3840', quality: 'high', n: 1 },
        },
      },
    }))
    mocks.loadKeys.mockResolvedValue({
      items: [{
        id: 1,
        name: 'Image key',
        key: 'sk-test',
        status: 'active',
        group: { name: 'Default', platform: 'openai', allow_image_generation: true },
      }],
    })
    mocks.listModels.mockResolvedValue([{ id: 'gpt-image-2' }])
    const wrapper = mount(ImageGenerationView, {
      global: { stubs: { Icon: true, RouterLink: true } },
    })
    await flushPromises()
    let selects = wrapper.findAllComponents(Select)

    expect(selects[3].props('modelValue')).toBe('9:16')
    expect(selects[4].props('modelValue')).toBe('2160x3840')

    selects[3].vm.$emit('update:modelValue', '16:9')
    await wrapper.vm.$nextTick()
    selects = wrapper.findAllComponents(Select)

    expect(selects[4].props('modelValue')).toBe('3840x2160')
    expect(selects[4].props('options').map((option: { value: string }) => option.value)).toEqual([
      'auto', '1536x864', '2048x1152', '3840x2160', '__custom_size__',
    ])
  })

	it('uses the persisted async result without overwriting it', async () => {
		mocks.loadKeys.mockResolvedValue({ items: [{ id: 1, name: 'Image key', key: 'sk-test', status: 'active', group: { name: 'Default', platform: 'openai', allow_image_generation: true } }] })
		mocks.listModels.mockResolvedValue([{ id: 'gpt-image-2' }])
		mocks.submitAsyncGeneration.mockResolvedValue({ id: 'task-1' })
		mocks.getTask.mockResolvedValue({ status: 'completed', result: { data: [{ url: '/v1/images/storage/images/result.png' }] } })
		mocks.getHistory.mockImplementation(async (id: string) => ({ id, sessionId: 'session-1', prompt: 'Test', status: 'completed', images: [{ url: '/api/v1/image-sessions/assets/test', mimeType: 'image/png' }], createdAt: 1 }))
		const wrapper = mount(ImageGenerationView, { global: { stubs: { Icon: true, RouterLink: true } } })
		await flushPromises()
		await wrapper.get('textarea').setValue('Test')
		await wrapper.get('form').trigger('submit')
		await flushPromises()
		expect(mocks.getHistory).toHaveBeenCalled()
		expect(mocks.cacheImages).not.toHaveBeenCalled()
		expect(mocks.saveHistory).toHaveBeenCalledTimes(1)
		wrapper.unmount()
	})
})
