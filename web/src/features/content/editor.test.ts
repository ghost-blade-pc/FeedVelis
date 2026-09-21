import { describe, expect, it } from 'vitest'

import { ApiError } from '../../api/client'
import { contentErrorMessage, insertAssetReference, isArticleVersionConflict } from './editor'

describe('文章编辑器状态', () => {
  it('按光标插入站内资产协议并保留周围 Markdown', () => {
    expect(insertAssetReference('开头结尾', 'asset-id', 2, 2)).toEqual({
      value: '开头\n\n![图片](asset:asset-id)\n\n结尾',
      cursor: 25,
    })
  })

  it('只把明确的文章版本冲突进入保留本地文本流程', () => {
    expect(isArticleVersionConflict(new ApiError('冲突', 409, 'ARTICLE_VERSION_CONFLICT'))).toBe(true)
    expect(isArticleVersionConflict(new ApiError('幂等键冲突', 409, 'IDEMPOTENCY_KEY_REUSED'))).toBe(false)
  })

  it('呈现管理员锁定和资产降级反馈', () => {
    expect(contentErrorMessage(new ApiError('', 403, 'ARTICLE_ADMIN_OFFLINE'))).toContain('管理员下架')
    expect(contentErrorMessage(new ApiError('', 503, 'ASSET_UNAVAILABLE'))).toContain('纯文本')
  })
})
