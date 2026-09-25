import { describe, expect, it } from 'vitest'

import { normalizeSearchForm, searchRouteQuery } from './search'

describe('搜索表单', () => {
  it('规范化查询且 URL 不包含 cursor', () => {
    const query = normalizeSearchForm({ q: '  中文 Go  ', keyword: '  关键词 ', topic: '', sourceID: '12' })
    expect(query).toEqual({ q: '中文 Go', keyword: '关键词', topic: undefined, source_id: 12, limit: 20 })
    expect(searchRouteQuery({ ...query, cursor: 'private-cursor' })).toEqual({ q: '中文 Go', keyword: '关键词', source_id: '12' })
  })

  it('按 Unicode 字符校验并拒绝非法来源', () => {
    expect(() => normalizeSearchForm({ q: '', keyword: '', topic: '', sourceID: '' })).toThrow('1 到 200')
    expect(() => normalizeSearchForm({ q: '界'.repeat(201), keyword: '', topic: '', sourceID: '' })).toThrow('1 到 200')
    expect(() => normalizeSearchForm({ q: 'go', keyword: '', topic: '', sourceID: '0' })).toThrow('正整数')
    expect(() => normalizeSearchForm({ q: 'go', keyword: '', topic: '', sourceID: '1.5' })).toThrow('正整数')
  })
})
