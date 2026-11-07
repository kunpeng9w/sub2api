import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import ChannelStatusV1View from '../ChannelStatusV1View.vue'
import MonitorHero from '@/components/user/monitor/MonitorHero.vue'
import MonitorDetailDialog from '@/components/user/MonitorDetailDialog.vue'

const mocks = vi.hoisted(() => ({ list: vi.fn(), run: vi.fn(), status: vi.fn(), showError: vi.fn(), showInfo: vi.fn() }))
vi.mock('@/api/channelMonitor', () => ({ list: mocks.list, run: mocks.run, status: mocks.status }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ cachedPublicSettings: { channel_monitor_enabled: true }, showError: mocks.showError, showInfo: mocks.showInfo }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string, values?: Record<string, unknown>) => values ? `${key} ${JSON.stringify(values)}` : key }),
}))
enableAutoUnmount(afterEach)
function items(ids = [1, 2, 3]) { return ids.map(id => ({ id, name: `Monitor ${id}`, can_run: true, primary_model: 'model', primary_status: 'operational', availability_7d: 99 })) }
function deferred() { let resolve!: () => void; const promise = new Promise<void>(done => { resolve = done }); return { promise, resolve } }
function mountView() {
  return shallowMount(ChannelStatusV1View, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    MonitorHero: false,
    MonitorCardGrid: false,
    MonitorCard: { props: ['item', 'running', 'runDisabled'], emits: ['run', 'click'], template: '<div><button class="single" :data-id="item.id" :disabled="running || runDisabled" @click="$emit(\'run\')">Run</button><button class="detail" @click="$emit(\'click\')">Detail</button></div>' },
  } } })
}
beforeEach(() => {
  vi.useFakeTimers(); vi.resetAllMocks(); localStorage.clear()
  localStorage.setItem('channel-status-auto-refresh', JSON.stringify({ enabled: false, interval_seconds: 30 }))
  mocks.list.mockResolvedValue({ items: items() }); mocks.run.mockResolvedValue(undefined)
  mocks.status.mockImplementation(async (id: number) => ({ id, models: [] }))
})
afterEach(() => { vi.useRealTimers(); localStorage.clear() })

describe('check all visible monitors', () => {
  it('places the action before refresh and processes a fresh visible list serially', async () => {
    const wrapper = mountView(); await flushPromises()
    const button = wrapper.get('[data-testid="run-all-monitors"]')
    expect(button.element.nextElementSibling?.getAttribute('title')).toBe('common.refresh')
    mocks.list.mockResolvedValue({ items: items([3, 7]) })
    const first = deferred(); mocks.run.mockReturnValueOnce(first.promise)
    await button.trigger('click'); await flushPromises()
    expect(mocks.run.mock.calls).toEqual([[3]])
    expect(wrapper.findComponent(MonitorHero).props('runAllTotal')).toBe(2)
    expect(button.attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('.single').every(single => single.attributes('disabled') !== undefined)).toBe(true)
    wrapper.findComponent(MonitorHero).vm.$emit('runAll'); await flushPromises()
    expect(mocks.run).toHaveBeenCalledTimes(1)
    first.resolve(); await flushPromises()
    expect(mocks.run.mock.calls).toEqual([[3], [7]])
    expect(mocks.showInfo).toHaveBeenCalledWith('channelStatus.runAllSummary {"completed":2,"failed":0,"skipped":0}', 6000)
    expect(wrapper.findComponent(MonitorDetailDialog).props('refreshVersion')).toBe(1)
    expect(mocks.list).toHaveBeenCalledTimes(3)
  })

  it('continues after errors and reports execution failures separately from skipped checks', async () => {
    mocks.list.mockResolvedValue({ items: [...items(), { ...items([4])[0], can_run: false }] })
    mocks.run.mockRejectedValueOnce({ status: 500 }).mockRejectedValueOnce({ reason: 'CHANNEL_MONITOR_COOLDOWN', status: 429 }).mockResolvedValueOnce(undefined)
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-testid="run-all-monitors"]').trigger('click'); await flushPromises()
    expect(mocks.run.mock.calls).toEqual([[1], [2], [3]])
    expect(mocks.showInfo).toHaveBeenCalledWith('channelStatus.runAllSummary {"completed":1,"failed":1,"skipped":2}', 6000)
  })

  it('shows progress and does not submit the remaining queue after leaving', async () => {
    const first = deferred(); const second = deferred()
    mocks.run.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-testid="run-all-monitors"]').trigger('click'); await flushPromises()
    first.resolve(); await flushPromises()
    expect(wrapper.get('[data-testid="run-all-monitors"]').text()).toContain('"done":1,"total":3')
    wrapper.unmount(); second.resolve(); await flushPromises()
    expect(mocks.run.mock.calls).toEqual([[1], [2]])
    expect(mocks.list).toHaveBeenCalledTimes(2)
    expect(mocks.showInfo).not.toHaveBeenCalled()
  })

  it('disables the action for no executable monitors and during an individual check', async () => {
    mocks.list.mockResolvedValueOnce({ items: [] })
    const empty = mountView(); await flushPromises()
    expect(empty.get('[data-testid="run-all-monitors"]').attributes('disabled')).toBeDefined()
    empty.unmount()
    const pending = deferred(); mocks.run.mockReturnValueOnce(pending.promise)
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('.single').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-testid="run-all-monitors"]').attributes('disabled')).toBeDefined()
    pending.resolve(); await flushPromises()
  })

  it('does not run checks when refreshing and refreshes open details after a batch', async () => {
    const wrapper = mountView(); await flushPromises()
    await wrapper.get('.detail').trigger('click')
    const dialog = wrapper.findComponent(MonitorDetailDialog)
    expect(dialog.props('show')).toBe(true)
    wrapper.findComponent(MonitorHero).vm.$emit('refresh'); await flushPromises()
    expect(mocks.run).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="run-all-monitors"]').trigger('click'); await flushPromises()
    expect(dialog.props('show')).toBe(true)
    expect(dialog.props('refreshVersion')).toBe(1)
  })
})
