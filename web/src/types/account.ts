export interface Account {
  id: string
  username: string
  nickname: string
  role: 'user' | 'admin'
  status: 'active' | 'disabled'
  created_at: string
}

/** 登录与刷新的响应；刷新令牌只通过 HttpOnly Cookie 传递，不进入正文。 */
export interface SessionPayload {
  access_token: string
  expires_at: string
  account: Account
}

export interface RegisterInput {
  username: string
  password: string
  nickname?: string
}
