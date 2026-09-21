export interface AdminSource {
  id: number
  feed_url: string
  title: string
  status: 'active' | 'paused'
  fetch_interval_seconds: number
  lock_version: number
  next_fetch_at: string | null
  last_success_at: string | null
  consecutive_failures: number
  last_error_code: string | null
  created_at: string
  updated_at: string
}

export interface SourcePage {
  items: AdminSource[]
  next_cursor: string | null
  has_more: boolean
}

export interface SourceFetchRun {
  id: string
  source_id: number
  trigger: 'scheduled' | 'manual'
  status: 'running' | 'succeeded' | 'failed' | 'aborted'
  not_modified: boolean
  inserted_count: number
  updated_count: number
  unchanged_count: number
  skipped_count: number
  error_code: string | null
  started_at: string
  completed_at: string | null
}

export interface SourceFetchRunPage {
  items: SourceFetchRun[]
  next_cursor: string | null
  has_more: boolean
}
