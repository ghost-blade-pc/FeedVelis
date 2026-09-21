import { describe, expect, it } from 'vitest'

import { ApiError } from '../../api/client'
import { fetchRunSummary, sourceErrorMessage } from './model'

describe('Source 管理反馈', () => {
  it('展示 304、统计和失败分类', () => {
    const base = { inserted_count: 1, updated_count: 2, unchanged_count: 3, skipped_count: 4, error_code: null }
    expect(fetchRunSummary({ ...base, status: 'succeeded', not_modified: true } as never)).toContain('304')
    expect(fetchRunSummary({ ...base, status: 'succeeded', not_modified: false } as never)).toContain('新增 1')
    expect(fetchRunSummary({ ...base, status: 'failed', not_modified: false, error_code: 'FETCH_TIMEOUT' } as never)).toContain('FETCH_TIMEOUT')
  })

  it('后端拒绝不会被导航可见性掩盖', () => {
    expect(sourceErrorMessage(new ApiError('', 403, 'AUTH_FORBIDDEN'))).toContain('管理员权限')
    expect(sourceErrorMessage(new ApiError('', 409, 'SOURCE_VERSION_CONFLICT'))).toContain('刷新')
  })
})
