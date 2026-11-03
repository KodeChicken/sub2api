<template>
  <section class="min-w-0 space-y-5" data-testid="kiro-authorization">
    <div class="flex items-center gap-3">
      <Icon name="link" class="text-primary-600" />
      <h4 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.accounts.kiro.signInTitle') }}</h4>
    </div>
    <fieldset :disabled="oauth.saving.value" class="min-w-0">
      <legend class="input-label">{{ t('admin.accounts.oauth.authMethod') }}</legend>
      <div class="flex flex-wrap gap-x-5 gap-y-2">
        <label v-for="option in methods" :key="option" class="flex items-center gap-2 text-sm">
          <input v-model="method" type="radio" :value="option" :name="`${id}-method`" />
          {{ t(`admin.accounts.kiro.method.${option}`) }}
        </label>
      </div>
    </fieldset>

    <template v-if="method === 'signin'">
      <fieldset :disabled="oauth.saving.value" class="space-y-3">
        <legend class="input-label">{{ t('admin.accounts.kiro.signInType') }}</legend>
        <div class="flex flex-wrap gap-x-5 gap-y-2">
          <label v-for="option in authTypes" :key="option" class="flex items-center gap-2 text-sm">
            <input v-model="authType" type="radio" :value="option" :name="`${id}-type`" />
            {{ t(`admin.accounts.kiro.login.${option}`) }}
          </label>
        </div>
        <div v-if="authType === 'iam_sso'">
          <label :for="`${id}-start`" class="input-label">{{ t('admin.accounts.kiro.startURL') }}</label>
          <input :id="`${id}-start`" v-model="startURL" class="input" type="url" placeholder="https://your-org.awsapps.com/start" autocomplete="off" />
        </div>
      </fieldset>
      <details v-if="!oauth.session.value" class="min-w-0">
        <summary class="cursor-pointer text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.kiro.advanced') }}</summary>
        <div class="mt-3 grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <label :for="`${id}-region`" class="input-label">{{ t('admin.accounts.kiro.auth_region') }}</label>
            <input :id="`${id}-region`" v-model="authRegion" class="input" autocomplete="off" />
          </div>
          <template v-if="!account">
            <div>
              <label :for="`${id}-endpoint`" class="input-label">{{ t('admin.accounts.kiro.endpoint') }}</label>
              <select :id="`${id}-endpoint`" v-model="settings.endpoint" class="input"><option value="ide">IDE</option><option value="cli">CLI</option></select>
            </div>
            <div>
              <label :for="`${id}-api-region`" class="input-label">{{ t('admin.accounts.kiro.api_region') }}</label>
              <input :id="`${id}-api-region`" v-model="settings.api_region" class="input" />
            </div>
            <div>
              <label :for="`${id}-domain`" class="input-label">{{ t('admin.accounts.kiro.api_domain') }}</label>
              <select :id="`${id}-domain`" v-model="settings.api_domain" class="input"><option value="q">AWS Q</option><option value="runtime">Kiro Runtime</option></select>
            </div>
          </template>
        </div>
      </details>
      <ol class="space-y-4 text-sm">
        <li class="space-y-2">
          <p class="font-medium">{{ t('admin.accounts.kiro.stepGenerate') }}</p>
          <button type="button" class="btn btn-primary" :disabled="oauth.loading.value || oauth.saving.value || (authType === 'iam_sso' && !startURL.trim())" @click="startSignIn">
            <Icon name="link" size="sm" />
            {{ t(oauth.session.value ? 'admin.accounts.kiro.restart' : 'admin.accounts.oauth.generateAuthUrl') }}
          </button>
        </li>
        <li v-if="oauth.session.value?.auth_url || oauth.session.value?.verification_uri" class="space-y-3">
          <p class="font-medium">{{ t('admin.accounts.kiro.stepAuthorize') }}</p>
          <div class="flex min-w-0 gap-2">
            <input :value="authorizationURL" readonly class="input min-w-0 flex-1 font-mono text-xs" :aria-label="t('admin.accounts.kiro.authorizationURL')" @focus="selectInput" />
            <button type="button" class="btn btn-secondary shrink-0" :title="t('common.copy')" :aria-label="t('common.copy')" @click="copyToClipboard(authorizationURL)"><Icon name="copy" size="sm" /></button>
            <a :href="authorizationURL" target="_blank" rel="noopener noreferrer" class="btn btn-secondary shrink-0" :title="t('admin.accounts.kiro.open')" :aria-label="t('admin.accounts.kiro.open')"><Icon name="externalLink" size="sm" /></a>
          </div>
          <div v-if="oauth.session.value?.user_code" class="flex items-center gap-3">
            <span class="font-mono text-xl font-semibold">{{ oauth.session.value.user_code }}</span>
            <button type="button" class="btn btn-secondary btn-sm" :title="t('common.copy')" :aria-label="t('common.copy')" @click="copyToClipboard(oauth.session.value.user_code)"><Icon name="copy" size="sm" /></button>
          </div>
        </li>
        <li v-if="authType === 'social' && oauth.session.value?.status === 'waiting'" class="space-y-2">
          <label :for="`${id}-callback`" class="block font-medium">{{ t('admin.accounts.kiro.stepCallback') }}</label>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.kiro.callbackNotice') }}</p>
          <textarea :id="`${id}-callback`" v-model="callbackURL" rows="3" maxlength="8192" class="input min-w-0 resize-y break-all font-mono text-xs" placeholder="http://127.0.0.1:3128/oauth/callback?code=...&amp;state=..." autocomplete="off" spellcheck="false" />
          <button type="button" class="btn btn-primary" :disabled="!callbackURL.trim() || oauth.loading.value" @click="oauth.complete(callbackURL)">
            <Icon name="check" size="sm" />{{ t('admin.accounts.oauth.completeAuth') }}
          </button>
        </li>
      </ol>
    </template>

    <template v-else>
      <template v-if="method === 'import'">
        <label :for="`${id}-json`" class="input-label">{{ t('admin.accounts.kiro.importCredentials') }}</label>
        <input type="file" accept=".json,application/json" :disabled="oauth.saving.value" @change="importFile" />
        <textarea :id="`${id}-json`" v-model="credentialJSON" rows="5" class="input min-w-0 font-mono text-xs" spellcheck="false" :disabled="oauth.saving.value" />
      </template>
      <KiroCredentialsFields v-else v-model="manualCredentials" account-type="oauth" />
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.kiro.entraImportOnly') }}</p>
      <button type="button" class="btn btn-primary" :disabled="oauth.loading.value || oauth.saving.value" @click="saveCredentials"><Icon name="check" size="sm" />{{ t('admin.accounts.oauth.completeAuth') }}</button>
    </template>

    <div v-if="oauth.loading.value || oauth.session.value" role="status" aria-live="polite" class="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300">
      <Icon v-if="oauth.loading.value" name="refresh" size="sm" class="animate-spin" />
      {{ t(`admin.accounts.kiro.state.${oauth.loading.value && !oauth.session.value ? 'starting' : oauth.session.value?.status || 'starting'}`) }}
    </div>
    <p v-if="oauth.error.value" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ errorMessage }}</p>
    <div v-if="oauth.session.value?.status === 'ready' || (oauth.saving.value && oauth.error.value)" class="flex gap-2">
      <button type="button" class="btn btn-primary" :disabled="oauth.loading.value" @click="oauth.saving.value ? oauth.check() : oauth.finalize()">
        <Icon name="refresh" size="sm" />{{ t('admin.accounts.kiro.retrySave') }}
      </button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import type { KiroAuthType, KiroOAuthAccount, KiroOAuthStart } from '@/api/admin/kiro'
import { useKiroOAuth } from '@/composables/useKiroOAuth'
import { useClipboard } from '@/composables/useClipboard'
import { buildKiroCredentials, normalizeKiroCredentials, parseKiroCredentialFile } from '@/utils/kiroCredentials'
import Icon from '@/components/icons/Icon.vue'
import KiroCredentialsFields from './KiroCredentialsFields.vue'

const props = defineProps<{ account?: Account | null; draft?: KiroOAuthAccount; configuration?: Record<string, unknown> }>()
const emit = defineEmits<{ authorized: [accountId: number] }>()
const { t, te } = useI18n()
const { copyToClipboard } = useClipboard()
const id = useId()
const methods = ['signin', 'import', 'manual'] as const
const authTypes = ['social', 'builder_id', 'iam_sso'] as const
const method = ref<typeof methods[number]>('signin')
const authType = ref<KiroAuthType>('social')
const startURL = ref('')
const authRegion = ref('us-east-1')
const settings = ref({ endpoint: 'ide', api_region: 'us-east-1', api_domain: 'q' })
const callbackURL = ref('')
const credentialJSON = ref('')
const manualCredentials = ref<Record<string, unknown>>(normalizeKiroCredentials({}))
const binding = () => JSON.stringify([props.account?.id || 0, props.account?.platform || 'kiro', props.draft])
const oauth = useKiroOAuth(binding, accountId => emit('authorized', accountId))
const authorizationURL = computed(() => oauth.session.value?.auth_url || oauth.session.value?.verification_uri_complete || oauth.session.value?.verification_uri || '')
const errorMessage = computed(() => {
  const key = `admin.accounts.kiro.errors.${oauth.error.value}`
  return te(key) ? t(key) : t('admin.accounts.kiro.errors.REQUEST_FAILED')
})
watch([method, authType, startURL, authRegion, () => props.account?.id, () => props.draft, () => props.configuration], () => {
  callbackURL.value = ''
  void oauth.reset()
})
watch(() => props.account, account => {
  const credentials = account?.credentials || {}
  authRegion.value = typeof credentials.auth_region === 'string' ? credentials.auth_region : 'us-east-1'
  startURL.value = typeof credentials.start_url === 'string' ? credentials.start_url : ''
  authType.value = credentials.auth_method === 'idc' ? (credentials.provider === 'Enterprise' ? 'iam_sso' : 'builder_id') : 'social'
}, { immediate: true })
const input = (type: KiroAuthType): KiroOAuthStart => props.account
  ? { intent: 'reauth', auth_type: type, account_id: props.account.id, auth_region: authRegion.value }
  : { intent: 'create', auth_type: type, account: props.draft, auth_region: authRegion.value }
const startSignIn = () => {
  const request = input(authType.value)
  if (!props.account) request.settings = { ...props.configuration, ...settings.value }
  if (authType.value === 'iam_sso') request.start_url = startURL.value.trim()
  void oauth.start(request)
}
const saveCredentials = () => {
  try {
    const credentials = method.value === 'import' ? parseKiroCredentialFile(credentialJSON.value) : buildKiroCredentials(manualCredentials.value)
    if (credentials.auth_method === 'api_key') throw new Error('type')
    const request = { ...input('credentials'), credentials }
    if (!props.account) request.settings = props.configuration
    void oauth.start(request)
  } catch {
    oauth.error.value = 'INVALID_CREDENTIALS'
  }
}
const importFile = async (event: Event) => {
  const target = event.target as HTMLInputElement
  const file = target.files?.[0]
  if (!file) return
  try {
    if (file.size > 1024 * 1024) throw new Error('size')
    credentialJSON.value = await file.text()
    parseKiroCredentialFile(credentialJSON.value)
  } catch {
    oauth.error.value = 'INVALID_CREDENTIALS'
  } finally {
    target.value = ''
  }
}
const selectInput = (event: FocusEvent) => (event.target as HTMLInputElement).select()
defineExpose({ saving: oauth.saving, reset: oauth.reset })
</script>
