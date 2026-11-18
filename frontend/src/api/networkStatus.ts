import { apiClient } from './client'

export type NetworkStatus = 'normal' | 'degraded' | 'failed' | 'untested' | 'stale' | 'disabled' | 'paused'
export type NetworkWindow = 1 | 7 | 30

export interface NetworkRules {
  enabled: boolean
  interval_seconds: number
  download_bytes: number
  upload_bytes: number
  repetitions: number
  connect_timeout_seconds: number
  request_timeout_seconds: number
  round_timeout_seconds: number
  daily_budget_bytes: number
  download_min_mbps: number
  upload_min_mbps: number
  min_success_ratio: number
  address_family: 'ipv4' | 'ipv6' | 'auto'
  http_version: 'http1.1' | 'http2'
}

export interface NetworkRoute {
  id: number
  name: string
  protocol: string
  accounts: { id: number; name: string }[]
  available: boolean
  unavailable_reason: string
  status: NetworkStatus
  checked_at: string | null
  download_mbps: number | null
  upload_mbps: number | null
  latency_ms: number | null
  success_rate: number | null
  sample_count: number
  timeline: { status: NetworkStatus; count: number; from: string; to: string }[]
}

export interface NetworkSnapshot {
  items: NetworkRoute[]
  rules: NetworkRules
  rule_version: string
  running: boolean
  next_run_at: string | null
  last_sync_at: string | null
  inventory_error: string
  worker_error: string
  budget_used_bytes: number
}

export interface NetworkSample {
  checked_at: string
  kind: 'download' | 'upload'
  requested_bytes: number
  transferred_bytes: number
  ok: boolean
  error: string
  http_code: number
  curl_exit: number
  speed_mbps: number | null
  duration_ms: number
  connect_ms: number
  tls_ms: number
  ttfb_ms: number
}

export interface NetworkHistory {
  route: Pick<NetworkRoute, 'id' | 'name' | 'protocol' | 'accounts'>
  runs: {
    id: number
    checked_at: string
    status: NetworkStatus
    rule_version: string
    route: NetworkHistory['route']
    rules: NetworkRules
    samples: NetworkSample[]
    skipped: string
  }[]
}

export async function fetchNetworkSnapshot(days: NetworkWindow, signal?: AbortSignal) {
  const { data } = await apiClient.get<NetworkSnapshot>('/network-status/snapshot', { params: { days }, signal })
  return data
}

export async function fetchNetworkHistory(id: number, days: NetworkWindow, signal?: AbortSignal) {
  const { data } = await apiClient.get<NetworkHistory>(`/network-status/routes/${id}/history`, { params: { days }, signal })
  return data
}

export async function saveNetworkRules(rules: NetworkRules) {
  const { data } = await apiClient.put<NetworkRules>('/admin/network-status/rules', rules)
  return data
}

export async function runNetworkDetection() {
  await apiClient.post('/admin/network-status/run')
}
