<template>
  <AppLayout>
    <MonitorHero
      :overall-status="overallStatus"
      :interval-seconds="DEFAULT_INTERVAL_SECONDS"
      :window="currentWindow"
      :loading="loading"
      :auto-refresh="autoRefresh"
      :running-all="runningAll"
      :run-all-disabled="loading || runningAll || runningIDs.size > 0 || !items.some(item => item.can_run)"
      :run-all-done="runAllDone"
      :run-all-total="runAllTotal"
      @run-all="runAllMonitors"
      @update:window="handleWindowChange"
      @refresh="refresh()"
    />

    <MonitorCardGrid
      :items="items"
      :running-i-ds="runningIDs"
      :run-disabled="runningAll"
      @run="runMonitor"
      :window="currentWindow"
      :countdown-seconds="countdown"
      :loading="loading"
      :detail-cache="detailCache"
      @card-click="openDetail"
    />

    <MonitorDetailDialog
      :show="showDetail"
      :refresh-version="detailRefreshVersion"
      :monitor-id="detailTarget?.id ?? null"
      :title="detailTitle"
      @close="closeDetail"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import {
  list as listChannelMonitorViews,
  run as runChannelMonitor,
  status as fetchChannelMonitorDetail,
  type UserMonitorView,
  type UserMonitorDetail,
} from '@/api/channelMonitor'
import AppLayout from '@/components/layout/AppLayout.vue'
import MonitorHero, {
  type MonitorWindow,
  type OverallStatus,
} from '@/components/user/monitor/MonitorHero.vue'
import MonitorCardGrid from '@/components/user/monitor/MonitorCardGrid.vue'
import MonitorDetailDialog from '@/components/user/MonitorDetailDialog.vue'
import { DEFAULT_INTERVAL_SECONDS, STATUS_OPERATIONAL } from '@/constants/channelMonitor'
import { useAutoRefresh } from '@/composables/useAutoRefresh'

const { t } = useI18n()
const appStore = useAppStore()

// ── State ──
const items = ref<UserMonitorView[]>([])
const loading = ref(false)
const currentWindow = ref<MonitorWindow>('7d')
const detailCache = reactive<Record<number, UserMonitorDetail>>({})
const showDetail = ref(false)
const detailRefreshVersion = ref(0)
const runningAll = ref(false)
const runAllDone = ref(0)
const runAllTotal = ref(0)
let disposed = false
const detailTarget = ref<UserMonitorView | null>(null)

let abortController: AbortController | null = null

const autoRefresh = useAutoRefresh({
  storageKey: 'channel-status-auto-refresh',
  intervals: [30, 60, 120] as const,
  defaultInterval: DEFAULT_INTERVAL_SECONDS,
  defaultEnabled: true,
  onRefresh: () => refresh(true),
  shouldPause: () => document.hidden || loading.value || runningAll.value,
})
const countdown = autoRefresh.countdown

// ── Computed ──
const overallStatus = computed<OverallStatus>(() => {
  if (items.value.length === 0) return 'operational'
  for (const it of items.value) {
    if (it.primary_status === 'failed' || it.primary_status === 'error') return 'degraded'
    if (it.primary_status !== STATUS_OPERATIONAL) return 'degraded'
  }
  return 'operational'
})

const detailTitle = computed(() => {
  return detailTarget.value?.name || t('channelStatus.detailTitle')
})

// ── Loaders ──
async function reload(silent = false) {
  if (abortController) abortController.abort()
  const ctrl = new AbortController()
  abortController = ctrl
  if (!silent) loading.value = true
  try {
    const res = await listChannelMonitorViews({ signal: ctrl.signal })
    if (ctrl.signal.aborted || abortController !== ctrl) return
    items.value = res.items || []
    const visibleIDs = new Set(items.value.map(item => item.id))
    for (const key of Object.keys(detailCache)) { if (!visibleIDs.has(Number(key))) delete detailCache[Number(key)] }
    if (detailTarget.value && !visibleIDs.has(detailTarget.value.id)) closeDetail()
  } catch (err: unknown) {
    const e = err as { name?: string; code?: string }
    if (e?.name === 'AbortError' || e?.code === 'ERR_CANCELED') return
    appStore.showError(extractApiErrorMessage(err, t('channelStatus.loadError')))
  } finally {
    if (abortController === ctrl) {
      if (!silent) loading.value = false
      autoRefresh.resetCountdown()
      abortController = null
    }
  }
}

async function refresh(silent = false) {
  await reload(silent)
  // After base reload, refresh any cached detail records so non-7d availability
  // values stay in sync without forcing the user to switch tabs again.
  if (currentWindow.value !== '7d') {
    await Promise.all(items.value.map(it => loadDetail(it.id, true)))
  }
}

async function loadDetail(id: number, force = false) {
  if (!force && detailCache[id]) return
  try {
    detailCache[id] = await fetchChannelMonitorDetail(id)
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('channelStatus.detailLoadError')))
  }
}

async function ensureDetailsForWindow() {
  if (currentWindow.value === '7d') return
  await Promise.all(items.value.map(it => loadDetail(it.id)))
}

const runningIDs = reactive(new Set<number>())
async function runMonitor(item: UserMonitorView) {
  if (disposed || runningAll.value || runningIDs.has(item.id)) return
  runningIDs.add(item.id)
  try { await runChannelMonitor(item.id); await refresh(true) }
  catch (error) { appStore.showError(extractApiErrorMessage(error, t('common.error'))) }
  finally { runningIDs.delete(item.id) }
}

async function runAllMonitors() {
  if (disposed || loading.value || runningAll.value || runningIDs.size > 0 || !items.value.some(item => item.can_run)) return
  runningAll.value = true
  runAllDone.value = 0
  runAllTotal.value = 0
  const totals = { completed: 0, failed: 0, skipped: 0 }
  try {
    // Fetch all currently visible monitors, independent of any cached card state.
    const latest = await listChannelMonitorViews()
    if (disposed) return
    items.value = latest.items || []
    const queue = [...items.value]
    runAllTotal.value = queue.length
    for (const item of queue) {
      if (disposed) break
      const visible = items.value.find(current => current.id === item.id)
      if (!visible?.can_run || runningIDs.has(item.id)) {
        totals.skipped++
        runAllDone.value++
        continue
      }
      runningIDs.add(item.id)
      try {
        await runChannelMonitor(item.id)
        totals.completed++
      } catch (error) {
        const code = extractApiErrorCode(error)
        const status = error && typeof error === 'object' && 'status' in error ? error.status : undefined
        if (status === 404 || status === 429 || ['CHANNEL_MONITOR_NOT_FOUND', 'CHANNEL_MONITOR_DISABLED', 'CHANNEL_MONITOR_BUSY', 'CHANNEL_MONITOR_COOLDOWN', 'CHANNEL_MONITOR_EGRESS_UNAVAILABLE'].includes(code || '')) totals.skipped++
        else totals.failed++
      } finally {
        runningIDs.delete(item.id)
        runAllDone.value++
      }
    }
    if (disposed) return
    appStore.showInfo(t('channelStatus.runAllSummary', totals), 6000)
    // Detail cache and an already open dialog both need fresh history.
    for (const id of Object.keys(detailCache)) delete detailCache[Number(id)]
    detailRefreshVersion.value++
    await refresh(true)
  } catch (error) {
    if (!disposed) appStore.showError(extractApiErrorMessage(error, t('channelStatus.loadError')))
  } finally {
    runningAll.value = false
  }
}

// ── Handlers ──
async function handleWindowChange(value: MonitorWindow) {
  currentWindow.value = value
  await ensureDetailsForWindow()
}

function openDetail(row: UserMonitorView) {
  detailTarget.value = row
  showDetail.value = true
}

function closeDetail() {
  showDetail.value = false
  detailTarget.value = null
}

watch(items, () => {
  void ensureDetailsForWindow()
})

watch(
  () => appStore.cachedPublicSettings?.channel_monitor_enabled,
  (enabled) => {
    if (enabled === false) autoRefresh.stop()
    else if (autoRefresh.enabled.value) autoRefresh.start()
  },
)

onMounted(() => {
  void reload(false)
  if (appStore.cachedPublicSettings?.channel_monitor_enabled !== false) {
    autoRefresh.setEnabled(autoRefresh.enabled.value)
  }
})

onBeforeUnmount(() => {
  disposed = true
  if (abortController) abortController.abort()
})
</script>
