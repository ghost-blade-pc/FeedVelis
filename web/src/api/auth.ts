import { request } from './client'
import type { Account, RegisterInput, SessionPayload } from '../types/account'

export function registerAccount(input: RegisterInput, signal?: AbortSignal): Promise<Account> {
  return request<Account>('/api/v1/auth/register', { method: 'POST', body: input, signal })
}

/** 登录非幂等：每次成功都会建立新会话，调用方不得自动重试。 */
export function login(username: string, password: string, signal?: AbortSignal): Promise<SessionPayload> {
  return request<SessionPayload>('/api/v1/auth/login', {
    method: 'POST',
    body: { username, password },
    signal,
  })
}

export function refreshSession(csrfToken: string, signal?: AbortSignal): Promise<SessionPayload> {
  return request<SessionPayload>('/api/v1/auth/refresh', {
    method: 'POST',
    body: {},
    signal,
    headers: { 'X-CSRF-Token': csrfToken },
  })
}

export function logoutSession(csrfToken: string, signal?: AbortSignal): Promise<void> {
  return request<void>('/api/v1/auth/logout', {
    method: 'POST',
    body: {},
    signal,
    headers: { 'X-CSRF-Token': csrfToken },
  })
}

export function fetchCurrentAccount(accessToken: string, signal?: AbortSignal): Promise<Account> {
  return request<Account>('/api/v1/account/me', { accessToken, signal })
}

export function updateNickname(accessToken: string, nickname: string, signal?: AbortSignal): Promise<Account> {
  return request<Account>('/api/v1/account/me', {
    method: 'PATCH',
    body: { nickname },
    accessToken,
    signal,
  })
}
