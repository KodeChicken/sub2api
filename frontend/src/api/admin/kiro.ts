import { apiClient } from '../client'

export type KiroAuthType = 'social' | 'builder_id' | 'iam_sso' | 'credentials'
export type KiroOAuthStatus =
  | 'starting' | 'waiting' | 'exchanging' | 'polling' | 'ready' | 'saving'
  | 'succeeded' | 'cancelled' | 'denied' | 'expired' | 'failed'

export interface KiroOAuthAccount {
  name: string
  notes?: string | null
  proxy_id?: number | null
  concurrency: number
  priority: number
  rate_multiplier?: number
  load_factor?: number | null
  group_ids: number[]
  expires_at?: number | null
  auto_pause_on_expired?: boolean
  extra?: Record<string, unknown>
}

export interface KiroOAuthStart {
  intent: 'create' | 'reauth'
  auth_type: KiroAuthType
  account_id?: number
  account?: KiroOAuthAccount
  auth_region?: string
  start_url?: string
  settings?: Record<string, unknown>
  credentials?: Record<string, unknown>
}

export interface KiroOAuthResult {
  session_id: string
  auth_type?: KiroAuthType
  status: KiroOAuthStatus
  error_code?: string
  expires_at?: string
  auth_url?: string
  user_code?: string
  verification_uri?: string
  verification_uri_complete?: string
  poll_interval?: number
  retry_after?: number
  account_id?: number
}

const base = '/admin/kiro/oauth/sessions'
const options = { timeout: 30_000 }

export const kiroAPI = {
  async start(input: KiroOAuthStart): Promise<KiroOAuthResult> {
    const { data } = await apiClient.post<KiroOAuthResult>(base, input, options)
    return data
  },
  async complete(id: string, callback_url: string, signal?: AbortSignal): Promise<KiroOAuthResult> {
    const { data } = await apiClient.post<KiroOAuthResult>(`${base}/${encodeURIComponent(id)}/complete`, { callback_url }, signal ? { ...options, signal } : options)
    return data
  },
  async poll(id: string, signal?: AbortSignal): Promise<KiroOAuthResult> {
    const { data } = await apiClient.post<KiroOAuthResult>(`${base}/${encodeURIComponent(id)}/poll`, undefined, signal ? { ...options, signal } : options)
    return data
  },
  async finalize(id: string): Promise<KiroOAuthResult> {
    const { data } = await apiClient.post<KiroOAuthResult>(`${base}/${encodeURIComponent(id)}/finalize`, undefined, options)
    return data
  },
  async status(id: string, signal?: AbortSignal): Promise<KiroOAuthResult> {
    const { data } = await apiClient.get<KiroOAuthResult>(`${base}/${encodeURIComponent(id)}`, signal ? { ...options, signal } : options)
    return data
  },
  async cancel(id: string): Promise<KiroOAuthResult> {
    const { data } = await apiClient.delete<KiroOAuthResult>(`${base}/${encodeURIComponent(id)}`, options)
    return data
  }
}

export default kiroAPI
