import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AccountProxyField from '../AccountProxyField.vue'

const { getEgressPolicy } = vi.hoisted(() => ({ getEgressPolicy: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getEgressPolicy } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

function render(extra = {}) {
  return mount(AccountProxyField, {
    props: { modelValue: null, proxies: [], platform: 'anthropic', accountType: 'oauth', ...extra },
    global: { stubs: { ProxySelector: { template: '<div data-testid="proxy-selector" />' } } }
  })
}

describe('account egress policy', () => {
  beforeEach(() => {
    getEgressPolicy.mockReset()
    getEgressPolicy.mockResolvedValue({ claude_egress_enabled: true, official_domains: ['anthropic.com', 'claude.ai', 'claude.com'] })
  })
  it('uses the managed indicator for official accounts without changing a binding automatically', async () => {
    const wrapper = render({ modelValue: 71 })
    await flushPromises()
    expect(wrapper.find('[data-testid="managed-claude-egress"]').exists()).toBe(true)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await wrapper.find('button').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([[null]])
  })
  it.each([
    { platform: 'openai' },
    { accountType: 'service_account' },
    { baseUrl: 'https://third-party.invalid/v1' },
    { customBaseUrl: 'https://relay.invalid' },
    { baseUrl: 'https://anthropic.com.attacker.invalid' }
  ])('keeps independent proxy selection for %j', async (props) => {
    const wrapper = render(props)
    await flushPromises()
    expect(wrapper.find('[data-testid="proxy-selector"]').exists()).toBe(true)
  })
  it('uses the active custom relay before a retained base URL, matching backend routing', async () => {
    const official = render({ baseUrl: 'https://third-party.invalid', customBaseUrl: 'https://api.anthropic.com' })
    const thirdParty = render({ baseUrl: 'https://api.anthropic.com', customBaseUrl: 'https://relay.invalid' })
    await flushPromises()
    expect(official.find('[data-testid="managed-claude-egress"]').exists()).toBe(true)
    expect(thirdParty.find('[data-testid="proxy-selector"]').exists()).toBe(true)
  })
  it('preserves phase-one UI when managed egress is disabled', async () => {
    getEgressPolicy.mockResolvedValue({ claude_egress_enabled: false, official_domains: [] })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.find('[data-testid="proxy-selector"]').exists()).toBe(true)
  })
})
