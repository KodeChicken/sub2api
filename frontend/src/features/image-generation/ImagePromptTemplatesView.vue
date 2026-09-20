<template>
  <div class="space-y-5">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <SearchInput v-model="search" placeholder="搜索模板标题或提示词" class="w-full sm:w-80" />
      <button class="btn btn-primary" @click="openCreate"><Icon name="plus" size="sm" class="mr-2" />新建模板</button>
    </div>
    <div v-if="filteredTemplates.length === 0" class="grid min-h-64 place-items-center text-center">
      <div><h2 class="text-base font-semibold text-gray-900 dark:text-white">还没有提示词模板</h2><p class="mt-2 text-sm text-gray-500">保存常用风格或场景提示词，在生图工作台中一键使用。</p></div>
    </div>
    <div v-else class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      <article v-for="template in filteredTemplates" :key="template.id" class="flex min-h-56 flex-col rounded-lg border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ template.title }}</h2>
        <p v-if="template.description" class="mt-1 text-xs text-gray-500">{{ template.description }}</p>
        <p class="mt-4 line-clamp-5 flex-1 text-sm leading-6 text-gray-700 dark:text-gray-300">{{ template.prompt }}</p>
        <div class="mt-4 flex items-center gap-2">
          <button class="btn btn-primary btn-sm" @click="useTemplate(template)"><Icon name="sparkles" size="xs" class="mr-1.5" />用于生图</button>
          <button class="btn btn-secondary btn-sm" @click="openEdit(template)">编辑</button>
          <button class="ml-auto rounded-md p-2 text-gray-400 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-950/30" title="删除" @click="remove(template.id)"><Icon name="trash" size="sm" /></button>
        </div>
      </article>
    </div>
    <BaseDialog :show="dialogOpen" :title="editingId ? '编辑提示词模板' : '新建提示词模板'" @close="dialogOpen = false">
      <div class="space-y-4">
        <label class="input-label block">模板名称<input v-model="title" class="input mt-1 w-full" maxlength="80" /></label>
        <label class="input-label block">说明<input v-model="description" class="input mt-1 w-full" maxlength="160" /></label>
        <label class="input-label block">提示词<textarea v-model="templatePrompt" class="input mt-1 min-h-36 w-full resize-y" maxlength="4000" /></label>
      </div>
      <template #footer><button class="btn btn-secondary" @click="dialogOpen = false">取消</button><button class="btn btn-primary" :disabled="!title.trim() || !templatePrompt.trim()" @click="save">保存</button></template>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import BaseDialog from '@/components/common/BaseDialog.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Icon from '@/components/icons/Icon.vue'
import type { ImagePromptTemplate } from './types'
import { createImagePromptTemplate, loadImagePromptTemplates, saveImagePromptTemplates } from './templates'

const router = useRouter()
const templates = ref(loadImagePromptTemplates())
const search = ref('')
const dialogOpen = ref(false)
const editingId = ref('')
const title = ref('')
const description = ref('')
const templatePrompt = ref('')
const filteredTemplates = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  return query ? templates.value.filter(item => `${item.title} ${item.description} ${item.prompt}`.toLocaleLowerCase().includes(query)) : templates.value
})
function openCreate() { editingId.value = ''; title.value = ''; description.value = ''; templatePrompt.value = ''; dialogOpen.value = true }
function openEdit(template: ImagePromptTemplate) { editingId.value = template.id; title.value = template.title; description.value = template.description; templatePrompt.value = template.prompt; dialogOpen.value = true }
function save() {
  if (editingId.value) templates.value = templates.value.map(item => item.id === editingId.value ? { ...item, title: title.value.trim(), description: description.value.trim(), prompt: templatePrompt.value.trim(), updatedAt: Date.now() } : item)
  else templates.value = [createImagePromptTemplate(title.value, templatePrompt.value, description.value), ...templates.value]
  saveImagePromptTemplates(templates.value); dialogOpen.value = false
}
function remove(id: string) { if (!window.confirm('确定删除这个提示词模板吗？')) return; templates.value = templates.value.filter(item => item.id !== id); saveImagePromptTemplates(templates.value) }
function useTemplate(template: ImagePromptTemplate) {
  sessionStorage.setItem('image-generation-draft-template-id', template.id)
  void router.push('/image-generation/create')
}
</script>
