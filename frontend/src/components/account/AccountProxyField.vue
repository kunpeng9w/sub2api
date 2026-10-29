<template>
  <div>
    <ProxySelector :model-value="modelValue" :proxies="proxies" @update:model-value="$emit('update:modelValue', $event)" />
    <div v-if="managed" class="input-hint" data-testid="claude-proxy-priority">
      {{ t('admin.accounts.claudeProxyPriorityHint') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import ProxySelector from '@/components/common/ProxySelector.vue'
import type { Proxy } from '@/types'

const props = defineProps<{
  modelValue: number | null
  proxies: Proxy[]
  platform?: string
  accountType?: string
  baseUrl?: string
  customBaseUrl?: string
}>()
defineEmits<{ 'update:modelValue': [number | null] }>()
const { t } = useI18n()
const enabled = ref(false)
const domains = ref<string[]>([])
const managed = computed(() => {
  if (!enabled.value || props.platform !== 'anthropic' || ['bedrock', 'service_account'].includes(props.accountType || '')) return false
  const raw = props.customBaseUrl?.trim() || props.baseUrl?.trim()
  if (!raw) return true
  try {
    const host = new URL(raw).hostname.toLowerCase().replace(/\.$/, '')
    return domains.value.some(domain => host === domain || host.endsWith(`.${domain}`))
  } catch { return false }
})
onMounted(async () => {
  try {
    const policy = await adminAPI.accounts.getEgressPolicy()
    enabled.value = policy.claude_egress_enabled
    domains.value = policy.official_domains
  } catch {
    // Server-side routing remains authoritative if capabilities cannot load.
    enabled.value = false
  }
})
</script>
