import { ApiError } from '../../api/client'

export function insertAssetReference(markdown: string, assetID: string, start: number, end = start): { value: string; cursor: number } {
  const reference = `![图片](asset:${assetID})`
  const prefix = start > 0 && markdown[start - 1] !== '\n' ? '\n\n' : ''
  const suffix = end < markdown.length && markdown[end] !== '\n' ? '\n\n' : ''
  const inserted = `${prefix}${reference}${suffix}`
  return { value: markdown.slice(0, start) + inserted + markdown.slice(end), cursor: start + prefix.length + reference.length }
}

export function isArticleVersionConflict(error: unknown): boolean {
  return error instanceof ApiError && error.status === 409 && error.code === 'ARTICLE_VERSION_CONFLICT'
}

export function contentErrorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === 'ARTICLE_ADMIN_OFFLINE') return '文章已被管理员下架，当前不能重新发布。'
    if (error.code === 'ASSET_UNAVAILABLE') return '图片服务暂不可用，纯文本内容仍可保存。'
    if (error.code === 'ASSET_QUOTA_EXCEEDED') return '图片额度已用尽，请删除未使用的图片后重试。'
    if (error.code === 'VALIDATION_FAILED') return error.message
    return error.message
  }
  return error instanceof Error ? error.message : '操作失败，请重试。'
}
