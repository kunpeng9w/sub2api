<template>
  <div v-if="estimate" class="text-[10px] text-gray-500 dark:text-gray-400" :title="t('monitorCommon.estimate.note')" data-testid="quota-estimate">
    <template v-if="valid && !expired">
      <span>{{ t('monitorCommon.estimate.total', { value: estimate.total_cost!.toFixed(2) }) }}</span>
      <span class="ml-2">{{ t('monitorCommon.estimate.remaining', { value: estimate.remaining_cost!.toFixed(2) }) }}</span>
    </template>
    <span v-else>{{ t('monitorCommon.estimate.unavailable') }}</span>
    <span v-if="estimate.sampled_at" class="ml-2">{{ t('monitorCommon.estimate.sampled', { time: sampledTime }) }}</span>
    <span class="block">{{ t('monitorCommon.estimate.note') }}</span>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useNow } from '@vueuse/core'
import type { QuotaEstimate } from '@/types'
const props = defineProps<{ estimate?: QuotaEstimate | null; resetsAt?: string | null }>()
const { t } = useI18n()
const now = useNow({ interval: 30000 })
const expired = computed(() => !!props.resetsAt && new Date(props.resetsAt).getTime() <= now.value.getTime())
const valid = computed(() => props.estimate?.total_cost != null && props.estimate?.remaining_cost != null && Number.isFinite(props.estimate.total_cost) && Number.isFinite(props.estimate.remaining_cost) && props.estimate.total_cost > 0 && props.estimate.remaining_cost >= 0)
const sampledTime = computed(() => {
  const date = new Date(props.estimate?.sampled_at || '')
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
})
</script>
