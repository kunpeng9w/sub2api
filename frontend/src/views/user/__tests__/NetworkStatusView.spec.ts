import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { fetchNetworkHistory, fetchNetworkSnapshot, runNetworkDetection, saveNetworkRules, type NetworkSnapshot } from '@/api/networkStatus'
import zh from '@/i18n/locales/zh'
import NetworkStatusView from '../NetworkStatusView.vue'

const user = vi.hoisted(() => ({ isAdmin: false }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => user }))
vi.mock('@/api/networkStatus', () => ({ fetchNetworkHistory: vi.fn(), fetchNetworkSnapshot: vi.fn(), runNetworkDetection: vi.fn(), saveNetworkRules: vi.fn() }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/components/icons/Icon.vue', () => ({ default: { template: '<span />' } }))

function fixture(): NetworkSnapshot {
  return {
    rules: { enabled: false, interval_seconds: 1800, download_bytes: 1000000, upload_bytes: 1000000, repetitions: 3, connect_timeout_seconds: 10, request_timeout_seconds: 30, round_timeout_seconds: 600, daily_budget_bytes: 500000000, download_min_mbps: 10, upload_min_mbps: 3, min_success_ratio: 1, address_family: 'ipv4', http_version: 'http1.1' },
    rule_version: 'test-version', running: false, next_run_at: null, last_sync_at: '2026-11-11T08:00:00Z', inventory_error: '', worker_error: '', budget_used_bytes: 0,
    items: [{ id: 0, name: 'Direct', protocol: 'direct', accounts: [{ id: 1, name: 'Linked account' }], available: true, unavailable_reason: '', status: 'untested', checked_at: null, download_mbps: null, upload_mbps: null, latency_ms: null, success_rate: null, sample_count: 0, timeline: [{ status: 'untested', count: 0, from: '2026-11-10T00:00:00Z', to: '2026-11-11T00:00:00Z' }] }],
  }
}

function render() {
  return mount(NetworkStatusView, { global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })] } })
}

describe('NetworkStatusView', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  beforeEach(() => {
    vi.clearAllMocks()
    user.isAdmin = false
    vi.mocked(fetchNetworkSnapshot).mockResolvedValue(fixture())
    vi.mocked(fetchNetworkHistory).mockResolvedValue({ route: { id: 0, name: 'Direct', protocol: 'direct', accounts: [] }, runs: [] })
    vi.mocked(runNetworkDetection).mockResolvedValue(undefined)
    vi.mocked(saveNetworkRules).mockResolvedValue(fixture().rules)
  })

  it('shows real linked accounts and missing measurements without invented speeds', async () => {
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('Linked account')
    expect(wrapper.text()).toContain('未检测')
    expect(wrapper.text()).not.toContain('86.4')
    expect(wrapper.find('[data-testid="run-detection"]').exists()).toBe(false)
    await wrapper.findAll('button').find(button => button.text() === '检测规则')!.trigger('click')
    expect(wrapper.find('input[type="number"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('button[type="submit"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('refreshes and changes history range without triggering a bandwidth test', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-window="30"]').trigger('click')
    await flushPromises()
    expect(fetchNetworkSnapshot).toHaveBeenLastCalledWith(30, expect.any(AbortSignal))
    await wrapper.findAll('button').find(button => button.text() === '详情')!.trigger('click')
    await flushPromises()
    expect(fetchNetworkHistory).toHaveBeenCalledWith(0, 30, expect.any(AbortSignal))
    expect(wrapper.get('[data-testid="route-detail"]').text()).toContain('尚无当前代理配置')
    expect(runNetworkDetection).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('allows administrators to save rules independently of starting a detection', async () => {
    user.isAdmin = true
    const wrapper = render()
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '检测规则')!.trigger('click')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(saveNetworkRules).toHaveBeenCalledWith(fixture().rules)
    expect(runNetworkDetection).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="run-detection"]').trigger('click')
    await flushPromises()
    expect(runNetworkDetection).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('reports an unavailable service and preserves previous data on a later refresh failure', async () => {
    vi.mocked(fetchNetworkSnapshot).mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('尚未配置或暂不可用')
    await wrapper.findAll('button').find(button => button.text() === '刷新结果')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Linked account')
    vi.mocked(fetchNetworkSnapshot).mockRejectedValueOnce(new Error('unavailable'))
    await wrapper.findAll('button').find(button => button.text() === '刷新结果')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('上次加载的数据')
    expect(wrapper.text()).toContain('Linked account')
    wrapper.unmount()
  })

  it('automatically recovers from initial and later transient failures', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    vi.mocked(fetchNetworkSnapshot).mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = render()
    try {
      await flushPromises()
      expect(wrapper.find('[role="alert"]').exists()).toBe(true)
      await vi.advanceTimersByTimeAsync(30000)
      await flushPromises()
      expect(wrapper.find('[role="alert"]').exists()).toBe(false)
      expect(wrapper.text()).toContain('Linked account')

      vi.mocked(fetchNetworkSnapshot).mockRejectedValueOnce(new Error('unavailable'))
      await vi.advanceTimersByTimeAsync(30000)
      await flushPromises()
      expect(wrapper.get('[role="alert"]').text()).toContain('上次加载的数据')
      const recovered = fixture()
      recovered.items[0].accounts[0].name = 'Recovered account'
      vi.mocked(fetchNetworkSnapshot).mockResolvedValue(recovered)
      await vi.advanceTimersByTimeAsync(30000)
      await flushPromises()
      expect(wrapper.find('[role="alert"]').exists()).toBe(false)
      expect(wrapper.text()).toContain('Recovered account')
      expect(runNetworkDetection).not.toHaveBeenCalled()
    } finally { wrapper.unmount() }
  })

  it('refreshes open history and discards an older in-flight response', async () => {
    const wrapper = render()
    try {
      await flushPromises()
      let resolveOld!: (value: Awaited<ReturnType<typeof fetchNetworkHistory>>) => void
      vi.mocked(fetchNetworkHistory).mockReturnValueOnce(new Promise(resolve => { resolveOld = resolve }))
      await wrapper.findAll('button').find(button => button.text() === '详情')!.trigger('click')
      const oldSignal = vi.mocked(fetchNetworkHistory).mock.calls[0][2]!
      const history = {
        route: { id: 0, name: 'Direct', protocol: 'direct', accounts: [] },
        runs: [{ id: 1, checked_at: '2026-11-11T08:00:00Z', status: 'normal' as const, rule_version: 'new',
          route: { id: 0, name: 'Direct', protocol: 'direct', accounts: [{ id: 2, name: 'New history account' }] },
          rules: fixture().rules, samples: [], skipped: '' }],
      }
      vi.mocked(fetchNetworkHistory).mockResolvedValue(history)
      await wrapper.findAll('button').find(button => button.text() === '刷新结果')!.trigger('click')
      await flushPromises()
      expect(oldSignal.aborted).toBe(true)
      expect(wrapper.get('[data-testid="route-detail"]').text()).toContain('New history account')
      resolveOld({ ...history, runs: [] })
      await flushPromises()
      expect(wrapper.get('[data-testid="route-detail"]').text()).toContain('New history account')

      // The same ID now has a different connection configuration and no history.
      vi.mocked(fetchNetworkHistory).mockResolvedValue({ ...history, runs: [] })
      await wrapper.findAll('button').find(button => button.text() === '刷新结果')!.trigger('click')
      await flushPromises()
      expect(wrapper.get('[data-testid="route-detail"]').text()).not.toContain('New history account')
      expect(wrapper.get('[data-testid="route-detail"]').text()).toContain('尚无当前代理配置')
    } finally { wrapper.unmount() }
  })

  it('refreshes open history on polling and closes it when the route disappears', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const wrapper = render()
    try {
      await flushPromises()
      await wrapper.findAll('button').find(button => button.text() === '详情')!.trigger('click')
      await flushPromises()
      await vi.advanceTimersByTimeAsync(30000)
      await flushPromises()
      expect(fetchNetworkHistory).toHaveBeenCalledTimes(2)
      vi.mocked(fetchNetworkSnapshot).mockResolvedValue({ ...fixture(), items: [] })
      await vi.advanceTimersByTimeAsync(30000)
      await flushPromises()
      expect(wrapper.find('[data-testid="route-detail"]').exists()).toBe(false)
    } finally { wrapper.unmount() }
  })
})
