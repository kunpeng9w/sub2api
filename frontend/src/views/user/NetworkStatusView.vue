<template>
  <AppLayout>
    <div class="network-status space-y-5 pb-8">
      <header class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 class="page-title">{{ t('networkStatus.title') }}</h1>
          <p class="page-description mt-1">{{ t('networkStatus.subtitle') }}</p>
        </div>
        <span v-if="snapshot?.running" class="badge badge-primary" role="status">{{ t('networkStatus.running') }}</span>
      </header>

      <div v-if="loadError" class="network-notice" role="alert">
        {{ t(snapshot ? 'networkStatus.loadFailed' : 'networkStatus.unavailable') }}
      </div>
      <div v-if="snapshot?.inventory_error" class="network-notice" role="alert">{{ t('networkStatus.inventoryError') }}</div>
      <div v-if="snapshot?.worker_error" class="network-notice" role="alert">{{ t('networkStatus.workerError') }}</div>

      <section v-if="snapshot" class="card flex flex-wrap items-center justify-between gap-4 p-4 sm:p-5" :aria-label="t('networkStatus.status')">
        <div class="flex items-center gap-3">
          <span class="network-overview-icon"><Icon name="globe" size="md" /></span>
          <div>
            <h2 class="font-semibold text-gray-900 dark:text-gray-100">{{ overallLabel }}</h2>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('networkStatus.summary', { routes: snapshot.items.length, accounts: accountCount }) }}</p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('networkStatus.updated', { time: formatTime(snapshot.last_sync_at) }) }}</p>
          </div>
        </div>
        <div class="flex flex-wrap gap-2">
          <span v-for="status in visibleStatuses" :key="status" :class="['network-badge', statusClass(status)]">{{ counts[status] }} {{ statusLabel(status) }}</span>
        </div>
      </section>

      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="tabs inline-flex" role="group" :aria-label="t('networkStatus.history')">
          <button v-for="days in windows" :key="days" type="button" class="tab" :class="{ 'tab-active': windowDays === days }" :aria-pressed="windowDays === days" :data-window="days" @click="setWindow(days)">
            {{ days === 1 ? t('networkStatus.oneDay') : t('networkStatus.days', { n: days }) }}
          </button>
        </div>
        <div class="flex flex-wrap gap-2">
          <button v-if="snapshot" type="button" class="btn btn-secondary text-sm" :aria-expanded="showRules" @click="toggleRules">{{ t('networkStatus.rules') }}</button>
          <button v-if="authStore.isAdmin && snapshot" type="button" class="btn btn-primary text-sm" :disabled="snapshot.running || runPending" data-testid="run-detection" @click="runDetection">{{ t(snapshot.running ? 'networkStatus.running' : 'networkStatus.run') }}</button>
          <button type="button" class="btn btn-secondary inline-flex items-center gap-1.5 text-sm" :disabled="loading" @click="loadSnapshot()">
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />{{ t('networkStatus.refresh') }}
          </button>
        </div>
      </div>

      <section v-if="showRules && draft" class="card p-4 sm:p-5" :aria-label="t('networkStatus.rules')">
        <h2 class="font-semibold">{{ t('networkStatus.rules') }}</h2>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('networkStatus.rulesHint') }}</p>
        <form class="mt-4" @submit.prevent="saveRules">
          <label class="mb-4 flex items-center gap-2 text-sm"><input v-model="draft.enabled" type="checkbox" :disabled="!authStore.isAdmin" class="rounded" />{{ t('networkStatus.enabled') }}</label>
          <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
            <label v-for="field in numericFields" :key="field.key" class="block text-xs text-gray-500 dark:text-gray-400">
              {{ t(`networkStatus.${field.key}`) }}
              <input v-model.number="draft[field.key]" type="number" class="input mt-1 w-full" :min="field.min" :max="field.max" :step="field.step || 1" :disabled="!authStore.isAdmin" required />
            </label>
            <label class="block text-xs text-gray-500 dark:text-gray-400">{{ t('networkStatus.address_family') }}
              <select v-model="draft.address_family" class="input mt-1 w-full" :disabled="!authStore.isAdmin"><option value="ipv4">IPv4</option><option value="ipv6">IPv6</option><option value="auto">{{ t('networkStatus.auto') }}</option></select>
            </label>
            <label class="block text-xs text-gray-500 dark:text-gray-400">{{ t('networkStatus.http_version') }}
              <select v-model="draft.http_version" class="input mt-1 w-full" :disabled="!authStore.isAdmin"><option value="http1.1">HTTP/1.1</option><option value="http2">HTTP/2</option></select>
            </label>
          </div>
          <p v-if="ruleError" class="mt-3 text-sm text-red-600 dark:text-red-400" role="alert">{{ ruleError }}</p>
          <button v-if="authStore.isAdmin" type="submit" class="btn btn-primary mt-4" :disabled="saving">{{ t('networkStatus.save') }}</button>
        </form>
      </section>

      <p v-if="loading && !snapshot" class="py-10 text-center text-sm text-gray-500" role="status">{{ t('common.loading') }}</p>
      <p v-else-if="snapshot && !snapshot.items.length" class="card p-8 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('networkStatus.empty') }}</p>

      <div v-if="snapshot?.items.length" class="network-table-shell">
        <div class="overflow-x-auto">
          <table class="network-table">
            <thead><tr><th>{{ t('networkStatus.route') }}</th><th>{{ t('networkStatus.status') }}</th><th class="text-right">{{ t('networkStatus.download') }} / {{ t('networkStatus.upload') }}</th><th>{{ t('networkStatus.history') }}</th><th><span class="sr-only">{{ t('networkStatus.detail') }}</span></th></tr></thead>
            <tbody>
              <tr v-for="route in snapshot.items" :key="route.id">
                <td class="network-route-cell">
                  <div class="font-semibold text-gray-900 dark:text-gray-100">{{ route.id === 0 ? t('networkStatus.direct') : route.name }}</div>
                  <div class="mt-1 flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400"><span class="network-protocol">{{ route.protocol.toUpperCase() }}</span><span>{{ t('networkStatus.checked', { time: formatTime(route.checked_at) }) }}</span></div>
                  <p class="mt-2 break-words text-xs text-gray-500 dark:text-gray-400"><span>{{ t('networkStatus.accountNames') }}：</span>{{ route.accounts.map(account => account.name).join('、') || t('networkStatus.noAccounts') }}</p>
                </td>
                <td>
                  <span :class="['network-badge', statusClass(route.status)]">{{ statusLabel(route.status) }}</span>
                  <p v-if="route.unavailable_reason" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t(`networkStatus.${route.unavailable_reason}`) }}</p>
                  <p v-else class="mt-1 text-xs tabular-nums text-gray-500 dark:text-gray-400">{{ value(route.latency_ms, 0) }} ms</p>
                </td>
                <td class="text-right tabular-nums"><div class="font-semibold">{{ value(route.download_mbps) }} <span class="text-xs font-normal text-gray-500">Mbps</span></div><div class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ value(route.upload_mbps) }} Mbps</div></td>
                <td class="network-history-cell">
                  <div class="flex items-center justify-between gap-2 text-xs"><span class="text-gray-500 dark:text-gray-400">{{ t('networkStatus.successRate') }}</span><span class="tabular-nums">{{ route.success_rate === null ? '—' : route.success_rate.toFixed(2) + '%' }}</span></div>
                  <div class="network-timeline" role="img" :aria-label="`${t('networkStatus.history')} · ${route.success_rate ?? '—'}%`">
                    <span v-for="(bucket, index) in route.timeline" :key="index" :class="['network-bucket', `network-bucket-${bucket.status}`]" :title="t('networkStatus.tooltip', { from: formatTime(bucket.from), to: formatTime(bucket.to), status: statusLabel(bucket.status), count: bucket.count })"></span>
                  </div>
                  <div class="mt-1 flex justify-between text-[11px] text-gray-400"><span>{{ t('networkStatus.samples', { n: route.sample_count }) }}</span><span>{{ windowDays === 1 ? t('networkStatus.oneDay') : t('networkStatus.days', { n: windowDays }) }}</span></div>
                </td>
                <td><button type="button" class="text-xs font-medium text-primary-600 hover:underline dark:text-primary-400" :aria-expanded="selectedID === route.id" @click="toggleDetail(route.id)">{{ t('networkStatus.detail') }}</button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <footer class="flex flex-wrap justify-between gap-2 px-4 py-3 text-xs text-gray-500 dark:text-gray-400"><span>{{ t('networkStatus.summary', { routes: snapshot.items.length, accounts: accountCount }) }}</span><span>{{ snapshot.next_run_at ? t('networkStatus.nextRun', { time: formatTime(snapshot.next_run_at) }) : t('networkStatus.manual') }}</span></footer>
      </div>

      <section v-if="selectedID !== null" class="card p-4 sm:p-5" :aria-label="t('networkStatus.detail')" data-testid="route-detail">
        <div class="mb-4 flex items-center justify-between gap-3"><h2 class="font-semibold">{{ t('networkStatus.detailTitle', { name: selectedName }) }}</h2><button type="button" class="text-sm text-primary-600 dark:text-primary-400" @click="closeDetail">{{ t('networkStatus.close') }}</button></div>
        <p v-if="detailLoading" class="text-sm text-gray-500" role="status">{{ t('networkStatus.detailLoading') }}</p>
        <p v-else-if="detailError" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ t('networkStatus.detailFailed') }}</p>
        <p v-else-if="!detail?.runs.length" class="text-sm text-gray-500 dark:text-gray-400">{{ t('networkStatus.detailEmpty') }}</p>
        <div v-else class="space-y-5">
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('networkStatus.detailLimit') }}</p>
          <article v-for="run in detail.runs" :key="run.id" class="border-t border-gray-100 pt-4 dark:border-dark-700">
            <div class="mb-2 flex flex-wrap items-center justify-between gap-2 text-xs"><span>{{ formatTime(run.checked_at) }} <span :class="['network-badge ml-2', statusClass(run.status)]">{{ statusLabel(run.status) }}</span></span><span class="text-gray-500 dark:text-gray-400">{{ t('networkStatus.ruleVersion', { version: run.rule_version }) }}</span></div>
            <p class="mb-2 text-xs text-gray-500 dark:text-gray-400">{{ t('networkStatus.accountNames') }}：{{ run.route.accounts.map(account => account.name).join('、') || t('networkStatus.noAccounts') }}</p>
            <p v-if="run.skipped" class="mb-2 text-xs text-amber-700 dark:text-amber-400">{{ t('networkStatus.skipped', { reason: t(run.skipped === 'budget' ? 'networkStatus.budgetLimit' : 'networkStatus.deadline') }) }}</p>
            <div class="overflow-x-auto"><table class="network-sample-table"><thead><tr><th>{{ t('networkStatus.time') }}</th><th>{{ t('networkStatus.download') }} / {{ t('networkStatus.upload') }}</th><th>{{ t('networkStatus.payload') }}</th><th>{{ t('networkStatus.duration') }}</th><th>{{ t('networkStatus.result') }}</th></tr></thead><tbody><tr v-for="(sample, index) in run.samples" :key="index"><td>{{ formatTime(sample.checked_at) }}</td><td>{{ t(`networkStatus.${sample.kind}`) }} · {{ value(sample.speed_mbps) }} Mbps</td><td>{{ value(sample.transferred_bytes / 1000000, 2) }} / {{ value(sample.requested_bytes / 1000000, 2) }} MB</td><td>{{ value(sample.duration_ms / 1000, 2) }} s</td><td :class="sample.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'">{{ sample.ok ? t('networkStatus.success') : t('networkStatus.sampleError', { reason: sample.error }) }}</td></tr></tbody></table></div>
          </article>
        </div>
      </section>

      <footer v-if="snapshot" class="space-y-2 text-xs text-gray-500 dark:text-gray-400">
        <div class="flex flex-wrap justify-between gap-3"><div class="flex flex-wrap gap-3"><span v-for="status in legendStatuses" :key="status" class="inline-flex items-center gap-1"><i :class="['network-legend-dot', `network-bucket-${status}`]"></i>{{ statusLabel(status) }}</span></div><span>{{ t('networkStatus.budget', { used: (snapshot.budget_used_bytes / 1000000).toFixed(1), limit: (snapshot.rules.daily_budget_bytes / 1000000).toFixed(1) }) }}</span></div>
        <p>{{ t('networkStatus.measurementNote') }}</p>
      </footer>
      <p class="text-sm text-primary-600 dark:text-primary-400" role="status" aria-live="polite">{{ actionMessage }}</p>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAuthStore } from '@/stores/auth'
import { fetchNetworkHistory, fetchNetworkSnapshot, runNetworkDetection, saveNetworkRules, type NetworkHistory, type NetworkRules, type NetworkSnapshot, type NetworkStatus, type NetworkWindow } from '@/api/networkStatus'

const { t, locale } = useI18n()
const authStore = useAuthStore()
const snapshot = ref<NetworkSnapshot | null>(null)
const windowDays = ref<NetworkWindow>(7)
const windows: NetworkWindow[] = [1, 7, 30]
const loading = ref(false)
const loadError = ref(false)
const showRules = ref(false)
const draft = ref<NetworkRules | null>(null)
const ruleError = ref('')
const saving = ref(false)
const runPending = ref(false)
const actionMessage = ref('')
const selectedID = ref<number | null>(null)
const detail = ref<NetworkHistory | null>(null)
const detailLoading = ref(false)
const detailError = ref(false)
let snapshotController: AbortController | null = null
let detailController: AbortController | null = null
let timer: ReturnType<typeof setInterval> | undefined
let disposed = false

const statuses: NetworkStatus[] = ['normal', 'degraded', 'failed', 'stale', 'disabled', 'paused', 'untested']
const legendStatuses: NetworkStatus[] = ['normal', 'degraded', 'failed', 'untested']
const counts = computed(() => Object.fromEntries(statuses.map(status => [status, snapshot.value?.items.filter(item => item.status === status).length || 0])) as Record<NetworkStatus, number>)
const visibleStatuses = computed(() => statuses.filter(status => counts.value[status] > 0))
const accountCount = computed(() => new Set(snapshot.value?.items.flatMap(item => item.accounts.map(account => account.id)) || []).size)
const selectedName = computed(() => selectedID.value === 0 ? t('networkStatus.direct') : snapshot.value?.items.find(item => item.id === selectedID.value)?.name || '')
const overallLabel = computed(() => {
  if (!snapshot.value?.items.length || snapshot.value.items.every(item => item.status === 'untested')) return t('networkStatus.noResults')
  return t(snapshot.value.items.every(item => item.status === 'normal') ? 'networkStatus.allNormal' : 'networkStatus.attention')
})

type NumericRule = { [K in keyof NetworkRules]: NetworkRules[K] extends number ? K : never }[keyof NetworkRules]
const numericFields: { key: NumericRule; min: number; max: number; step?: number }[] = [
  { key: 'interval_seconds', min: 300, max: 86400 },
  { key: 'download_bytes', min: 1000, max: 100000000 },
  { key: 'upload_bytes', min: 1000, max: 100000000 },
  { key: 'repetitions', min: 1, max: 5 },
  { key: 'connect_timeout_seconds', min: 1, max: 60 },
  { key: 'request_timeout_seconds', min: 1, max: 120 },
  { key: 'round_timeout_seconds', min: 30, max: 3600 },
  { key: 'daily_budget_bytes', min: 1000000, max: 10000000000 },
  { key: 'download_min_mbps', min: 0, max: 100000, step: 0.1 },
  { key: 'upload_min_mbps', min: 0, max: 100000, step: 0.1 },
  { key: 'min_success_ratio', min: 0, max: 1, step: 0.01 },
]

function statusLabel(status: NetworkStatus) { return t(`networkStatus.${status}`) }
function statusClass(status: NetworkStatus) {
  return ({ normal: 'network-badge-normal', degraded: 'network-badge-degraded', failed: 'network-badge-failed', stale: 'network-badge-muted', disabled: 'network-badge-muted', paused: 'network-badge-muted', untested: 'network-badge-muted' })[status]
}
function value(number: number | null, digits = 1) { return number === null || !Number.isFinite(number) ? '—' : number.toFixed(digits) }
function formatTime(time: string | null) {
  if (!time) return t('networkStatus.never')
  const date = new Date(time)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat(locale.value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(date)
}

async function loadSnapshot(silent = false) {
  snapshotController?.abort()
  const controller = new AbortController()
  snapshotController = controller
  if (!silent) loading.value = true
  try {
    const data = await fetchNetworkSnapshot(windowDays.value, controller.signal)
    if (disposed || controller.signal.aborted) return
    snapshot.value = data
    loadError.value = false
    if (selectedID.value !== null && !data.items.some(item => item.id === selectedID.value)) closeDetail()
  } catch {
    if (!disposed && !controller.signal.aborted) loadError.value = true
  } finally {
    if (snapshotController === controller) { loading.value = false; snapshotController = null }
  }
}

function setWindow(days: NetworkWindow) {
  if (windowDays.value === days) return
  windowDays.value = days
  void loadSnapshot()
  if (selectedID.value !== null) void loadDetail(selectedID.value)
}

function toggleRules() {
  showRules.value = !showRules.value
  if (showRules.value && snapshot.value) draft.value = { ...snapshot.value.rules }
  ruleError.value = ''
}

async function saveRules() {
  if (!authStore.isAdmin || !draft.value || saving.value) return
  ruleError.value = ''
  if (numericFields.some(field => !Number.isFinite(draft.value![field.key]) || draft.value![field.key] < field.min || draft.value![field.key] > field.max) || draft.value.connect_timeout_seconds > draft.value.request_timeout_seconds) {
    ruleError.value = t('networkStatus.invalidRules')
    return
  }
  saving.value = true
  try {
    await saveNetworkRules({ ...draft.value })
    actionMessage.value = t('networkStatus.saved')
    showRules.value = false
    await loadSnapshot()
  } catch { ruleError.value = t('networkStatus.invalidRules') }
  finally { saving.value = false }
}

async function runDetection() {
  if (!authStore.isAdmin || snapshot.value?.running || runPending.value) return
  runPending.value = true
  actionMessage.value = ''
  try {
    await runNetworkDetection()
    actionMessage.value = t('networkStatus.queued')
    await loadSnapshot(true)
  } catch { actionMessage.value = t('networkStatus.actionFailed') }
  finally { runPending.value = false }
}

async function loadDetail(id: number) {
  detailController?.abort()
  const controller = new AbortController()
  detailController = controller
  detail.value = null
  detailError.value = false
  detailLoading.value = true
  try {
    const data = await fetchNetworkHistory(id, windowDays.value, controller.signal)
    if (!disposed && !controller.signal.aborted && selectedID.value === id) detail.value = data
  } catch { if (!disposed && !controller.signal.aborted) detailError.value = true }
  finally { if (detailController === controller) detailLoading.value = false }
}
function toggleDetail(id: number) {
  if (selectedID.value === id) { closeDetail(); return }
  selectedID.value = id
  void loadDetail(id)
}
function closeDetail() { detailController?.abort(); selectedID.value = null; detail.value = null }

onMounted(() => {
  void loadSnapshot()
  timer = setInterval(() => { if (!document.hidden && !loading.value && !showRules.value && !loadError.value) void loadSnapshot(true) }, 30000)
})
onBeforeUnmount(() => { disposed = true; snapshotController?.abort(); detailController?.abort(); if (timer) clearInterval(timer) })
</script>

<style scoped>
.network-table-shell { @apply overflow-hidden rounded-2xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800; }
.network-table { @apply w-full text-left text-sm; min-width: 720px; }
.network-table th { @apply bg-gray-50 px-4 py-3 text-xs font-medium text-gray-500 dark:bg-dark-900/40 dark:text-gray-400; }
.network-table td { @apply border-b border-gray-100 px-4 py-5 align-middle dark:border-dark-700; }
.network-route-cell { width: 34%; min-width: 210px; max-width: 350px; }
.network-history-cell { width: 25%; min-width: 170px; }
.network-protocol { @apply rounded bg-gray-100 px-1.5 py-0.5 text-[10px] font-medium dark:bg-dark-700; }
.network-badge { @apply inline-flex items-center whitespace-nowrap rounded-full px-2.5 py-1 text-xs font-medium; }
.network-badge-normal { @apply bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300; }
.network-badge-degraded { @apply bg-amber-50 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300; }
.network-badge-failed { @apply bg-red-50 text-red-700 dark:bg-red-500/15 dark:text-red-300; }
.network-badge-muted { @apply bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300; }
.network-timeline { display: grid; grid-template-columns: repeat(28, minmax(0, 1fr)); gap: 3px; @apply mt-2 h-4; }
.network-bucket { @apply rounded-sm; }
.network-bucket-normal { @apply bg-emerald-500/80 dark:bg-emerald-400/80; }
.network-bucket-degraded { @apply bg-amber-400 dark:bg-amber-500; }
.network-bucket-failed { @apply bg-red-400 dark:bg-red-500; }
.network-bucket-untested, .network-bucket-stale, .network-bucket-disabled, .network-bucket-paused { @apply bg-gray-200 dark:bg-dark-600; }
.network-legend-dot { @apply inline-block h-2 w-2 rounded-sm; }
.network-notice { @apply rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200; }
.network-overview-icon { @apply flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-blue-50 text-blue-600 dark:bg-blue-900/30 dark:text-blue-400; }
.network-sample-table { @apply w-full text-left text-xs; min-width: 540px; }
.network-sample-table th { @apply bg-gray-50 p-2 font-medium text-gray-500 dark:bg-dark-900/40 dark:text-gray-400; }
.network-sample-table td { @apply border-b border-gray-100 p-2 tabular-nums dark:border-dark-700; }
</style>
