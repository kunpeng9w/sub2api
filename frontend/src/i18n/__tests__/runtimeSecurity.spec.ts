import { describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'

// Exercise the same runtime entry selected by Vite and Vitest, without an i18n mock.
describe('i18n runtime security and compatibility', () => {
  it('loads string messages lazily and preserves interpolation, plurals, links and fallback', () => {
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      fallbackLocale: 'en',
      missingWarn: false,
      fallbackWarn: false,
      messages: {}
    })
    i18n.global.setLocaleMessage('en', {
      greeting: 'Hello {name}',
      count: 'no requests | one request | {count} requests',
      label: 'Account',
      linked: '@:label settings',
      fallback: 'English fallback'
    })
    expect(i18n.global.t('greeting', { name: 'Ada' })).toBe('Hello Ada')
    expect(i18n.global.t('count', 2)).toBe('2 requests')
    expect(i18n.global.t('linked')).toBe('Account settings')
    i18n.global.setLocaleMessage('zh', { greeting: '你好，{name}' })
    i18n.global.locale.value = 'zh'
    expect(i18n.global.t('greeting', { name: 'Ada' })).toBe('你好，Ada')
    expect(i18n.global.t('fallback')).toBe('English fallback')
  })

  it.each([
    ['message markup', '<iframe srcdoc="<b>test</b>"></iframe>', {}],
    ['named parameter', '<iframe srcdoc="{content}"></iframe>', { content: '<b>test</b>' }]
  ] as const)('neutralizes iframe srcdoc in %s when escapeParameter is enabled', (_, message, parameters) => {
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      escapeParameter: true,
      warnHtmlMessage: false,
      messages: { en: { message } }
    })
    // Inspect a detached template: no iframe is attached or executed.
    const template = document.createElement('template')
    template.innerHTML = i18n.global.t('message', parameters)
    const frame = template.content.querySelector('iframe')
    expect(frame).not.toBeNull()
    expect(frame?.hasAttribute('srcdoc')).toBe(false)
  })

  it('interpolates strings without dynamic Function compilation under CSP', () => {
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en: { greeting: 'Hello {name}' } }
    })
    const dynamicFunction = vi.fn(() => {
      throw new Error('unsafe-eval is blocked by CSP')
    })
    vi.stubGlobal('Function', dynamicFunction)
    try {
      expect(i18n.global.t('greeting', { name: 'CSP' })).toBe('Hello CSP')
      expect(dynamicFunction).not.toHaveBeenCalled()
    } finally {
      vi.unstubAllGlobals()
    }
  })
})
