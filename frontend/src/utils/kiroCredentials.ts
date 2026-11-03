import type { AccountListItem, AccountType } from '@/types'

export function isKiroMixedCandidate(account: AccountListItem, groupId: number): boolean {
  if (account.platform !== 'kiro' || account.extra?.mixed_scheduling !== true ||
      account.status !== 'active' || !account.schedulable ||
      !account.group_ids?.includes(groupId) || !['oauth', 'apikey'].includes(account.type)) return false
  const mapping = account.credentials?.model_mapping
  if (!mapping || typeof mapping !== 'object' || Array.isArray(mapping)) return false
  const concrete = (value: unknown): value is string => typeof value === 'string' &&
    value.length > 0 && value.length <= 256 && value.trim() === value &&
    !value.includes('*') && !Array.from(value).some(char => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127)
  return Object.entries(mapping).some(([key, value]) => concrete(key) && concrete(value))
}

const aliases: Record<string, string> = {
  accessToken: 'access_token',
  refreshToken: 'refresh_token',
  expiresAt: 'expires_at',
  profileArn: 'profile_arn',
  authMethod: 'auth_method',
  clientId: 'client_id',
  clientSecret: 'client_secret',
  authRegion: 'auth_region',
  apiRegion: 'api_region',
  machineId: 'machine_id',
  kiroApiKey: 'api_key',
  kiro_api_key: 'api_key',
  apiDomain: 'api_domain',
  kiroVersion: 'kiro_version',
  tokenEndpoint: 'token_endpoint',
  startUrl: 'start_url',
  issuerUrl: 'issuer_url'
}

export function normalizeKiroCredentials(input: Record<string, unknown>): Record<string, unknown> {
  const result = { ...input }
  for (const [alias, key] of Object.entries(aliases)) {
    if (!(key in result) && alias in result) result[key] = result[alias]
    delete result[alias]
  }
  let method = String(result.auth_method || '').toLowerCase()
  if (['builderid', 'builder_id', 'enterprise', 'sso', 'iam_sso'].includes(method)) method = 'idc'
  if (['github', 'google'].includes(method)) method = 'social'
  if (['azure', 'azuread', 'entra', 'entra_id'].includes(method)) method = 'external_idp'
  if (method === 'apikey') method = 'api_key'
  if (!method) method = result.api_key ? 'api_key' : result.client_id || result.provider === 'BuilderId' ? 'idc' : 'social'
  return {
    ...result,
    auth_method: method,
    endpoint: result.endpoint || (method === 'api_key' ? 'cli' : 'ide'),
    auth_region: result.auth_region || result.region || 'us-east-1',
    api_region: result.api_region || result.region || 'us-east-1',
    api_domain: result.api_domain || 'q'
  }
}

export function parseKiroCredentialFile(text: string): Record<string, unknown> {
  let input: unknown = JSON.parse(text)
  if (Array.isArray(input)) {
    if (input.length !== 1) throw new Error('singleAccount')
    input = input[0]
  }
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw new Error('invalidCredentials')
  const object = input as Record<string, unknown>
  const credentials = object.credentials
  const result = normalizeKiroCredentials(
    credentials && typeof credentials === 'object' && !Array.isArray(credentials)
      ? credentials as Record<string, unknown>
      : object
  )
  if (!result.refresh_token && !result.api_key) throw new Error('invalidCredentials')
  return result
}

export function kiroAccountType(credentials: Record<string, unknown>): AccountType {
  return credentials.auth_method === 'api_key' ? 'apikey' : 'oauth'
}

export function buildKiroCredentials(input: Record<string, unknown>): Record<string, unknown> {
  const result = normalizeKiroCredentials(input)
  for (const key of ['access_token', 'refresh_token', 'api_key', 'client_secret']) {
    if (typeof result[key] === 'string' && !result[key].trim()) delete result[key]
  }
  return result
}
