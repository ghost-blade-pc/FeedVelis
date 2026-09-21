import { ApiError } from '../../api/client'
import type { AdminSource, SourceFetchRun } from '../../types/source'

export function sourceStatusLabel(source: AdminSource): string {
  return source.status === 'paused' ? '已暂停' : '运行中'
}

export function fetchRunSummary(run: SourceFetchRun): string {
  if (run.status === 'running') return '抓取进行中'
  if (run.status === 'failed') return `抓取失败${run.error_code ? `：${run.error_code}` : ''}`
  if (run.status === 'aborted') return '抓取已中止'
  if (run.not_modified) return '上游未修改（304）'
  return `新增 ${run.inserted_count}，更新 ${run.updated_count}，未变化 ${run.unchanged_count}，跳过 ${run.skipped_count}`
}

export function sourceErrorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 403) return '当前账户没有管理员权限。'
    if (error.status === 409 && error.code === 'SOURCE_VERSION_CONFLICT') return 'Source 已被其他操作修改，请刷新后重试。'
    return error.message
  }
  return error instanceof Error ? error.message : 'Source 操作失败。'
}
