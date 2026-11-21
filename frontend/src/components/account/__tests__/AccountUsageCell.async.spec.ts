import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountUsageCell from '../AccountUsageCell.vue'
import type { Account, AccountUsageInfo } from '@/types'
import { buildAccountUsageRefreshKey } from '@/utils/accountUsageRefresh'

const { getUsage, queued } = vi.hoisted(() => ({
  getUsage: vi.fn(),
  queued: [] as Array<{ accountID: number; run: () => Promise<unknown> }>
}))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getUsage } } }))
vi.mock('@/utils/usageLoadQueue', () => ({
  enqueueUsageRequest: (account: Account, fetch: () => Promise<unknown>) => new Promise((resolve, reject) => {
    queued.push({ accountID: account.id, run: () => fetch().then(resolve, reject) })
  })
}))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const account = (id: number) => ({ id, name: `account-${id}`, platform: 'openai', type: 'oauth', extra: {} }) as Account
const usage = (percent: number): AccountUsageInfo => ({
  five_hour: { utilization: percent, resets_at: null, remaining_seconds: 0 },
  seven_day: null, seven_day_sonnet: null
})
const stubs = {
  UsageProgressBar: { props: ['utilization'], template: '<div data-test="percent">{{ utilization }}</div>' },
  OpenAIQuotaResetCell: { template: '<div><slot name="pre-actions" /></div>' }
}
type UsageCell = {
  loadActiveUsage: () => Promise<void>
  _usageCache: Map<number, { data: AccountUsageInfo; ts: number; refreshKey: string }>
}

beforeEach(() => {
  getUsage.mockReset()
  queued.length = 0
  Object.defineProperty(window, 'matchMedia', { writable: true, value: vi.fn(() => ({
    matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn()
  })) })
})

describe('quota request identity and generation', () => {
  it('captures the identity before queueing and rejects late old-account responses and cache writes', async () => {
    const old = deferred<AccountUsageInfo>()
    getUsage.mockReturnValueOnce(old.promise).mockResolvedValueOnce(usage(22))
    const wrapper = mount(AccountUsageCell, { props: { account: account(99101) }, global: { stubs } })
    expect(queued[0]?.accountID).toBe(99101)
    await wrapper.setProps({ account: account(99102) })
    expect(queued[1]?.accountID).toBe(99102)
    expect(queued).toHaveLength(2)
    const oldRun = queued[0]!.run()
    expect(getUsage).toHaveBeenLastCalledWith(99101)
    await queued[1]!.run()
    await flushPromises()
    expect(wrapper.get('[data-test="percent"]').text()).toBe('22')
    old.resolve(usage(99))
    await oldRun
    await flushPromises()
    expect(wrapper.get('[data-test="percent"]').text()).toBe('22')
    const cache = (wrapper.vm as unknown as UsageCell)._usageCache
    expect(cache.get(99102)?.data.five_hour?.utilization).toBe(22)
    expect(cache.has(99101)).toBe(false)
    wrapper.unmount()
  })

  it('keeps the new snapshot and cache when an old response for the same account arrives late', async () => {
    const old = deferred<AccountUsageInfo>()
    getUsage.mockReturnValueOnce(old.promise).mockResolvedValueOnce(usage(33))
    const original = account(99107)
    const wrapper = mount(AccountUsageCell, { props: { account: original }, global: { stubs } })
    const oldRun = queued[0]!.run()

    const next = { ...original, extra: { codex_5h_used_percent: 33 } }
    await wrapper.setProps({ account: next })
    expect(queued).toHaveLength(2)
    await queued[1]!.run()
    await flushPromises()
    expect(wrapper.get('[data-test="percent"]').text()).toBe('33')

    old.resolve(usage(99))
    await oldRun
    await flushPromises()
    expect(wrapper.get('[data-test="percent"]').text()).toBe('33')
    const cache = (wrapper.vm as unknown as UsageCell)._usageCache
    expect(cache.get(original.id)?.data.five_hour?.utilization).toBe(33)
    expect(cache.get(original.id)?.refreshKey).toBe(buildAccountUsageRefreshKey(next))
    wrapper.unmount()
  })

  it.each(['resolve', 'reject'] as const)('keeps the newer loading, error and display state when the old request ends: %s', async (outcome) => {
    const old = deferred<AccountUsageInfo>()
    const newer = deferred<AccountUsageInfo>()
    getUsage.mockReturnValueOnce(old.promise).mockReturnValueOnce(newer.promise)
    const wrapper = mount(AccountUsageCell, { props: { account: account(outcome === 'resolve' ? 99103 : 99104) }, global: { stubs } })
    const first = queued[0]!.run()
    const active = (wrapper.vm as unknown as UsageCell).loadActiveUsage()
    await flushPromises()
    if (outcome === 'resolve') old.resolve(usage(99)); else old.reject(new Error('old failure'))
    await first
    await flushPromises()
    expect(wrapper.attributes('aria-busy')).toBe('true')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    newer.resolve(usage(33))
    await active
    await flushPromises()
    expect(wrapper.get('[data-test="percent"]').text()).toBe('33')
    expect(wrapper.attributes('aria-busy')).toBe('false')
    wrapper.unmount()
  })

  it('does not publish or cache a result after unmount', async () => {
    const old = deferred<AccountUsageInfo>()
    getUsage.mockReturnValueOnce(old.promise)
    const wrapper = mount(AccountUsageCell, { props: { account: account(99105) }, global: { stubs } })
    const run = queued[0]!.run()
    const cache = (wrapper.vm as unknown as UsageCell)._usageCache
    wrapper.unmount()
    old.resolve(usage(99))
    await run
    await flushPromises()
    expect(cache.has(99105)).toBe(false)
    expect(wrapper.emitted('usage-loaded')).toBeUndefined()
  })

  it('shows each window sample independently and leaves missing sample time unknown', async () => {
    const wrapper = mount(AccountUsageCell, {
      props: {
        account: account(99106), requestBatchedUsage: vi.fn(),
        batchedUsage: { ...usage(12), updated_at: '2026-11-17T12:00:00Z',
          five_hour: { ...usage(12).five_hour!, sampled_at: '2026-11-17T11:00:00Z' },
          seven_day: { utilization: 75, resets_at: null, remaining_seconds: 0 } }
      }, global: { stubs }
    })
    expect(wrapper.get('[data-test="openai-observed-5h"] time').attributes('datetime')).toBe('2026-11-17T11:00:00Z')
    expect(wrapper.get('[data-test="openai-observed-7d"]').text()).toContain('quotaObservationUnknown')
    expect(wrapper.get('[data-test="openai-observed-7d"]').find('time').exists()).toBe(false)
    wrapper.unmount()
  })
})
