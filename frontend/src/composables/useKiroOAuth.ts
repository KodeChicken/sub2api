import { computed, onBeforeUnmount, ref } from 'vue'
import { kiroAPI, type KiroOAuthResult, type KiroOAuthStart } from '@/api/admin/kiro'

export function useKiroOAuth(binding: () => string, onSuccess: (accountId: number) => void) {
  const session = ref<KiroOAuthResult | null>(null)
  const loading = ref(false)
  const error = ref('')
  const saving = computed(() => session.value?.status === 'saving')
  let generation = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  let activeRequest: AbortController | undefined
  let completed = false

  const clearTimer = () => {
    if (timer) clearTimeout(timer)
    timer = undefined
  }
  const current = (epoch: number, key: string) => generation === epoch && binding() === key
  const errorCode = (err: unknown) => {
    const reason = (err as { reason?: string })?.reason
    return reason?.startsWith('KIRO_OAUTH_') ? reason.slice('KIRO_OAUTH_'.length) : 'REQUEST_FAILED'
  }
  const reset = async (unmount = false) => {
    if (saving.value && !unmount) return false
    const wasSaving = saving.value
    const prior = session.value
    const id = prior?.session_id
    generation++
    activeRequest?.abort()
    activeRequest = undefined
    const epoch = generation
    const key = binding()
    clearTimer()
    loading.value = false
    completed = false
    session.value = null
    error.value = ''
    if (prior?.status === 'succeeded') return true
    if (id && !unmount) {
      try {
        const result = await kiroAPI.cancel(id)
        if (!current(epoch, key)) return false
        if (result.status === 'saving' || result.status === 'succeeded') {
          session.value = result
          if (result.status === 'saving') schedule(epoch, key, 2, check)
          else if (result.account_id) {
            completed = true
            onSuccess(result.account_id)
          }
          return false
        }
      } catch (err) {
        if (!current(epoch, key)) return false
        session.value = prior
        error.value = errorCode(err) === 'SAVE_IN_PROGRESS' ? 'SAVE_IN_PROGRESS' : 'CANCEL_FAILED'
        if (error.value === 'SAVE_IN_PROGRESS' && prior) {
          session.value = { ...prior, status: 'saving' }
          schedule(epoch, key, 2, check)
        }
        return false
      }
    } else if (id && !wasSaving) {
      void kiroAPI.cancel(id).catch(() => {})
    }
    return true
  }

  const schedule = (epoch: number, key: string, seconds: number, run: () => Promise<void>) => {
    clearTimer()
    timer = setTimeout(() => {
      if (current(epoch, key)) void run()
    }, Math.max(1, seconds) * 1000)
  }
  const accept = async (result: KiroOAuthResult, epoch: number, key: string): Promise<void> => {
    if (!current(epoch, key)) return
    clearTimer()
    session.value = result
    loading.value = false
    error.value = result.error_code || ''
    if (result.status === 'ready' && !result.error_code) {
      await finalize()
    } else if (result.status === 'succeeded' && result.account_id && !completed) {
      completed = true
      onSuccess(result.account_id)
    } else if (result.status === 'waiting' && result.auth_type !== 'social') {
      schedule(epoch, key, result.retry_after || result.poll_interval || 5, poll)
    } else if (['starting', 'exchanging', 'polling', 'saving'].includes(result.status)) {
      schedule(epoch, key, Math.min(result.retry_after || 2, 5), check)
    }
  }
  const request = async (run: (id: string, signal?: AbortSignal) => Promise<KiroOAuthResult>, committing = false) => {
    const id = session.value?.session_id
    if (!id || loading.value) return
    const epoch = generation
    const key = binding()
    loading.value = true
    error.value = ''
    const controller = committing ? undefined : new AbortController()
    if (controller) activeRequest = controller
    if (committing && session.value) session.value = { ...session.value, status: 'saving' }
    try {
      const result = controller ? await run(id, controller.signal) : await run(id)
      await accept(result, epoch, key)
    } catch (err) {
      if (!current(epoch, key)) return
      error.value = errorCode(err)
      loading.value = false
      if (committing || saving.value) schedule(epoch, key, 2, check)
    } finally {
      if (activeRequest === controller) activeRequest = undefined
      if (current(epoch, key)) loading.value = false
    }
  }
  const check = async () => request(kiroAPI.status)
  const poll = async () => request(kiroAPI.poll)
  const finalize = async () => request(kiroAPI.finalize, true)
  const complete = async (callback: string) => request((id, signal) => kiroAPI.complete(id, callback.trim(), signal))
  const start = async (input: KiroOAuthStart) => {
    const epoch = generation + 1
    if (!(await reset()) || epoch !== generation) return
    const key = binding()
    loading.value = true
    try {
      const result = await kiroAPI.start(input)
      if (!current(epoch, key)) {
        void kiroAPI.cancel(result.session_id).catch(() => {})
        return
      }
      await accept(result, epoch, key)
    } catch (err) {
      if (current(epoch, key)) error.value = errorCode(err)
    } finally {
      if (current(epoch, key)) loading.value = false
    }
  }
  onBeforeUnmount(() => { void reset(true) })
  return { session, loading, error, saving, start, complete, finalize, check, reset }
}
