<template>
  <div v-if="estimate" class="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10px] leading-4 tabular-nums text-gray-500 dark:text-gray-400" data-testid="quota-estimate">
    <template v-if="valid && !expired">
      <span class="whitespace-nowrap">{{ t('monitorCommon.estimate.total', { value: estimate.total_cost!.toFixed(2) }) }}</span>
      <span class="whitespace-nowrap">{{ t('monitorCommon.estimate.remaining', { value: estimate.remaining_cost!.toFixed(2) }) }}</span>
    </template>
    <span v-else>{{ t('monitorCommon.estimate.unavailable') }}</span>
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
</script>
