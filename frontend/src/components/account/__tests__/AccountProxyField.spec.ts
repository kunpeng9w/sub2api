import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AccountProxyField from '../AccountProxyField.vue'

const { getEgressPolicy } = vi.hoisted(() => ({ getEgressPolicy: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getEgressPolicy } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

function render(extra = {}) {
  return mount(AccountProxyField, {
    props: { modelValue: null, proxies: [], platform: 'anthropic', accountType: 'oauth', ...extra },
    global: { stubs: { ProxySelector: { name: 'ProxySelector', template: '<div data-testid="proxy-selector" />' } } }
  })
}

describe('account egress policy', () => {
  beforeEach(() => {
    getEgressPolicy.mockReset()
    getEgressPolicy.mockResolvedValue({ claude_egress_enabled: true, official_domains: ['anthropic.com', 'claude.ai', 'claude.com'] })
  })
  it.each([null, 71])('allows official accounts to select a proxy while retaining binding %s', async (modelValue) => {
    const wrapper = render({ modelValue })
    await flushPromises()
    const selector = wrapper.findComponent({ name: 'ProxySelector' })
    expect(selector.exists()).toBe(true)
    expect(wrapper.find('[data-testid="claude-proxy-priority"]').text()).toBe('admin.accounts.claudeProxyPriorityHint')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    selector.vm.$emit('update:modelValue', 72)
    expect(wrapper.emitted('update:modelValue')).toEqual([[72]])
    selector.vm.$emit('update:modelValue', null)
    expect(wrapper.emitted('update:modelValue')).toEqual([[72], [null]])
    expect(wrapper.find('button').exists()).toBe(false)
  })
  it.each([
    { platform: 'openai' },
    { accountType: 'bedrock' },
    { accountType: 'service_account' },
    { baseUrl: 'https://third-party.invalid/v1' },
    { customBaseUrl: 'https://relay.invalid' },
    { baseUrl: 'https://anthropic.com.attacker.invalid' }
  ])('keeps normal proxy selection without the Claude hint for %j', async (props) => {
    const wrapper = render(props)
    await flushPromises()
    expect(wrapper.find('[data-testid="proxy-selector"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="claude-proxy-priority"]').exists()).toBe(false)
  })
  it('uses the active custom relay before a retained base URL, matching backend routing', async () => {
    const official = render({ baseUrl: 'https://third-party.invalid', customBaseUrl: 'https://api.anthropic.com' })
    const thirdParty = render({ baseUrl: 'https://api.anthropic.com', customBaseUrl: 'https://relay.invalid' })
    await flushPromises()
    expect(official.find('[data-testid="claude-proxy-priority"]').exists()).toBe(true)
    expect(thirdParty.find('[data-testid="claude-proxy-priority"]').exists()).toBe(false)
    expect(thirdParty.find('[data-testid="proxy-selector"]').exists()).toBe(true)
  })
  it('preserves normal proxy selection when managed egress is disabled', async () => {
    getEgressPolicy.mockResolvedValue({ claude_egress_enabled: false, official_domains: [] })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.find('[data-testid="proxy-selector"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="claude-proxy-priority"]').exists()).toBe(false)
  })
  it('keeps proxy selection available if policy capabilities cannot load', async () => {
    getEgressPolicy.mockRejectedValue(new Error('unavailable'))
    const wrapper = render({ modelValue: 71 })
    await flushPromises()
    expect(wrapper.find('[data-testid="proxy-selector"]').exists()).toBe(true)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })
})
