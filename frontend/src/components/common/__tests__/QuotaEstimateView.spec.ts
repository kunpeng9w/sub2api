import { mount } from '@vue/test-utils'
import { describe, it, expect, vi } from 'vitest'
import QuotaEstimateView from '../QuotaEstimateView.vue'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, values?: Record<string, unknown>) => `${key} ${values?.value ?? ''}` }) }))
describe('QuotaEstimateView', () => {
  it('renders shared backend estimates without calculating or querying', () => {
    const wrapper = mount(QuotaEstimateView, { props: { estimate: { total_cost: 30, remaining_cost: 18, sampled_at: '2026-11-05T10:00:00Z' } } })
    expect(wrapper.text()).toContain('30.00')
    expect(wrapper.text()).toContain('18.00')
    wrapper.unmount()
  })
  it('does not present expired or unavailable estimates as zero', () => {
    const wrapper = mount(QuotaEstimateView, { props: { estimate: { total_cost: 30, remaining_cost: 18 }, resetsAt: '2000-01-01T00:00:00Z' } })
    expect(wrapper.text()).toContain('unavailable')
    expect(wrapper.text()).not.toContain('30.00')
    wrapper.unmount()
  })
})
