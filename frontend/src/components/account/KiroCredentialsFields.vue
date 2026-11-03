<template>
  <section class="space-y-4" data-testid="kiro-credentials">
    <div class="flex items-center justify-between gap-3">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">Kiro</h3>
      <button type="button" class="btn btn-secondary btn-sm" @click="fileInput?.click()">
        <Icon name="upload" size="sm" />
        {{ t('admin.accounts.kiro.importCredentials') }}
      </button>
      <input ref="fileInput" type="file" accept=".json,application/json" class="hidden" @change="importFile" />
    </div>
    <p v-if="importError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ importError }}</p>
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <div>
        <label :for="`${id}-auth`" class="input-label">{{ t('admin.accounts.kiro.authMethod') }}</label>
        <select :id="`${id}-auth`" class="input" :value="method" :disabled="accountType === 'apikey'" @change="changeMethod">
          <option value="social">GitHub / Google</option>
          <option value="idc">Builder ID / AWS IdC</option>
          <option value="external_idp">Microsoft Entra ID</option>
          <option v-if="!accountType || accountType === 'apikey'" value="api_key">Kiro API Key</option>
        </select>
      </div>
      <div>
        <label :for="`${id}-endpoint`" class="input-label">{{ t('admin.accounts.kiro.endpoint') }}</label>
        <select :id="`${id}-endpoint`" class="input" :value="modelValue.endpoint || 'ide'" @change="updateEvent('endpoint', $event)">
          <option value="ide">IDE</option>
          <option value="cli">CLI</option>
        </select>
      </div>
      <div v-for="field in fields" :key="field.key">
        <label :for="`${id}-${field.key}`" class="input-label">{{ t(`admin.accounts.kiro.${field.key}`) }}</label>
        <input
          :id="`${id}-${field.key}`"
          :value="modelValue[field.key] || ''"
          :type="field.secret ? 'password' : 'text'"
          :required="!editing && field.required"
          :placeholder="editing && field.secret ? t('admin.accounts.kiro.unchanged') : undefined"
          :autocomplete="field.secret ? 'new-password' : 'off'"
          spellcheck="false"
          class="input min-w-0"
          @input="updateEvent(field.key, $event)"
        />
      </div>
      <div>
        <label :for="`${id}-domain`" class="input-label">{{ t('admin.accounts.kiro.api_domain') }}</label>
        <select :id="`${id}-domain`" class="input" :value="modelValue.api_domain || 'q'" @change="updateEvent('api_domain', $event)">
          <option value="q">AWS Q</option>
          <option value="runtime">Kiro Runtime</option>
        </select>
      </div>
    </div>
    <div>
      <label class="input-label">{{ t('admin.accounts.modelWhitelist') }}</label>
      <ModelWhitelistSelector v-model="models" platform="kiro" :account-id="accountId" />
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import ModelWhitelistSelector from './ModelWhitelistSelector.vue'
import { parseKiroCredentialFile } from '@/utils/kiroCredentials'

const props = defineProps<{
  modelValue: Record<string, unknown>
  editing?: boolean
  accountType?: string
  accountId?: number
}>()
const emit = defineEmits<{ 'update:modelValue': [value: Record<string, unknown>] }>()
const { t } = useI18n()
const id = useId()
const fileInput = ref<HTMLInputElement | null>(null)
const importError = ref('')
const models = computed({
  get: () => Object.keys((props.modelValue.model_mapping as Record<string, string>) || {}),
  set: (ids: string[]) => {
    const previous = (props.modelValue.model_mapping as Record<string, string>) || {}
    const next = { ...props.modelValue }
    if (ids.length) next.model_mapping = Object.fromEntries(ids.map(id => [id, previous[id] || id]))
    else delete next.model_mapping
    emit('update:modelValue', next)
  }
})
const method = computed(() => props.modelValue.auth_method || (props.accountType === 'apikey' ? 'api_key' : 'social'))
const fields = computed(() => {
  const result = method.value === 'api_key'
    ? [{ key: 'api_key', secret: true, required: true }]
    : [
        { key: 'refresh_token', secret: true, required: true },
        { key: 'access_token', secret: true, required: false }
      ]
  if (method.value === 'idc' || method.value === 'external_idp') {
    result.push({ key: 'client_id', secret: false, required: true })
  }
  if (method.value === 'idc') result.push({ key: 'client_secret', secret: true, required: true })
  if (method.value === 'external_idp') {
    result.push({ key: 'token_endpoint', secret: false, required: true }, { key: 'scopes', secret: false, required: false })
  }
  return [...result, ...['auth_region', 'api_region', 'profile_arn', 'machine_id', 'kiro_version']
    .map(key => ({ key, secret: false, required: false }))]
})

function updateEvent(key: string, event: Event) {
  emit('update:modelValue', { ...props.modelValue, [key]: (event.target as HTMLInputElement).value })
}

function changeMethod(event: Event) {
  const auth_method = (event.target as HTMLSelectElement).value
  emit('update:modelValue', {
    ...props.modelValue,
    auth_method,
    endpoint: auth_method === 'api_key' ? 'cli' : 'ide'
  })
}

async function importFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  importError.value = ''
  try {
    if (file.size > 1024 * 1024) throw new Error('invalidCredentials')
    const credentials = parseKiroCredentialFile(await file.text())
    if (props.accountType && (credentials.auth_method === 'api_key') !== (props.accountType === 'apikey')) {
      throw new Error('accountTypeMismatch')
    }
    emit('update:modelValue', { ...props.modelValue, ...credentials })
  } catch (error) {
    const reason = error instanceof Error && ['singleAccount', 'accountTypeMismatch'].includes(error.message)
      ? error.message : 'invalidCredentials'
    importError.value = t(`admin.accounts.kiro.${reason}`)
  } finally {
    input.value = ''
  }
}
</script>
