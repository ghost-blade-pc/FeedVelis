import { describe, expect, it } from 'vitest'

import { readCsrfToken } from './browser'

describe('readCsrfToken', () => {
  it('优先读取生产 __Host- 前缀 Cookie', () => {
    const cookie = '__Host-velis_csrf=prod-value; other=1'
    expect(readCsrfToken(cookie)).toBe('prod-value')
  })

  it('本地开发读取无前缀 Cookie', () => {
    expect(readCsrfToken('velis_csrf=dev-value; velis_refresh=token')).toBe('dev-value')
  })

  it('缺少 Cookie 时返回 null', () => {
    expect(readCsrfToken('')).toBeNull()
    expect(readCsrfToken('velis_refresh=token')).toBeNull()
  })
})
