#!/usr/bin/env bash
set -euo pipefail

# I2 可复现 HTTP 闭环。只对显式给出的开发/测试 API 执行，会创建用户、文章和 Source。
# 前置：auth/registration 已开启；jq、curl 可用；管理员账号已初始化。
: "${I2_E2E_ADMIN_USERNAME:?请设置 I2_E2E_ADMIN_USERNAME}"
: "${I2_E2E_ADMIN_PASSWORD:?请设置 I2_E2E_ADMIN_PASSWORD}"
: "${I2_E2E_FEED_URL:?请设置 API 可安全访问的公开 HTTP(S) Feed URL}"

api_base="${I2_E2E_API_BASE:-http://127.0.0.1:8080}"
origin="${I2_E2E_ORIGIN:-http://localhost:5173}"
case "$api_base" in
  http://127.0.0.1:*|http://localhost:*|https://*.test/*) ;;
  *)
    printf '拒绝对非本地/非 .test API 执行。请检查 I2_E2E_API_BASE=%s\n' "$api_base" >&2
    exit 2
    ;;
esac

new_key() { tr 'A-F' 'a-f' </proc/sys/kernel/random/uuid; }
json_header=(-H 'Content-Type: application/json' -H "Origin: $origin")
stamp="$(date +%s)"
username="i2_e2e_${stamp}"
password="Velis-${stamp}-Aa1!"

printf '1/7 注册并登录临时用户 %s\n' "$username"
curl -fsS "${json_header[@]}" -X POST "$api_base/api/v1/auth/register" \
  --data "$(jq -cn --arg u "$username" --arg p "$password" '{username:$u,password:$p,nickname:"I2 演示作者"}')" >/dev/null
user_login="$(curl -fsS "${json_header[@]}" -X POST "$api_base/api/v1/auth/login" \
  --data "$(jq -cn --arg u "$username" --arg p "$password" '{username:$u,password:$p}')")"
user_token="$(jq -er '.access_token' <<<"$user_login")"
admin_login="$(curl -fsS "${json_header[@]}" -X POST "$api_base/api/v1/auth/login" \
  --data "$(jq -cn --arg u "$I2_E2E_ADMIN_USERNAME" --arg p "$I2_E2E_ADMIN_PASSWORD" '{username:$u,password:$p}')")"
admin_token="$(jq -er '.access_token' <<<"$admin_login")"

printf '2/7 用户直接发布并匿名读取\n'
created="$(curl -fsS "${json_header[@]}" -H "Authorization: Bearer $user_token" \
  -H "Idempotency-Key: $(new_key)" -X POST "$api_base/api/v1/me/articles" \
  --data '{"title":"I2 直接发布演示","markdown":"第一版正文","initial_status":"published"}')"
article_id="$(jq -er '.id' <<<"$created")"
version="$(jq -er '.lock_version' <<<"$created")"
curl -fsS "$api_base/api/v1/articles/$article_id" | jq -e '.origin.type == "user" and .content_html != ""' >/dev/null

printf '3/7 编辑并模拟双标签版本冲突\n'
edited="$(curl -fsS "${json_header[@]}" -H "Authorization: Bearer $user_token" \
  -H "Idempotency-Key: $(new_key)" -H "If-Match: \"$version\"" \
  -X PATCH "$api_base/api/v1/me/articles/$article_id" --data '{"title":"I2 直接发布演示","markdown":"第二版正文"}')"
new_version="$(jq -er '.lock_version' <<<"$edited")"
conflict_file="$(mktemp)"
trap 'rm -f "$conflict_file"' EXIT
conflict_status="$(curl -sS -o "$conflict_file" -w '%{http_code}' "${json_header[@]}" \
  -H "Authorization: Bearer $user_token" -H "Idempotency-Key: $(new_key)" -H "If-Match: \"$version\"" \
  -X PATCH "$api_base/api/v1/me/articles/$article_id" --data '{"title":"旧标签覆盖","markdown":"不得提交"}')"
test "$conflict_status" = 409
jq -e '.error.code == "ARTICLE_VERSION_CONFLICT"' "$conflict_file" >/dev/null
version="$new_version"

printf '4/7 作者下架并重新发布\n'
offline="$(curl -fsS "${json_header[@]}" -H "Authorization: Bearer $user_token" \
  -H "Idempotency-Key: $(new_key)" -H "If-Match: \"$version\"" -X POST \
  "$api_base/api/v1/me/articles/$article_id/offline" --data '{}')"
version="$(jq -er '.lock_version' <<<"$offline")"
test "$(curl -sS -o /dev/null -w '%{http_code}' "$api_base/api/v1/articles/$article_id")" = 404
published="$(curl -fsS "${json_header[@]}" -H "Authorization: Bearer $user_token" \
  -H "Idempotency-Key: $(new_key)" -H "If-Match: \"$version\"" -X POST \
  "$api_base/api/v1/me/articles/$article_id/publish" --data '{}')"
version="$(jq -er '.lock_version' <<<"$published")"

printf '5/7 管理员下架并恢复投稿\n'
admin_offline="$(curl -fsS "${json_header[@]}" -H "Authorization: Bearer $admin_token" \
  -H "Idempotency-Key: $(new_key)" -H "If-Match: \"$version\"" -X POST \
  "$api_base/api/v1/admin/articles/$article_id/offline" --data '{}')"
version="$(jq -er '.lock_version' <<<"$admin_offline")"
test "$(curl -sS -o /dev/null -w '%{http_code}' "$api_base/api/v1/articles/$article_id")" = 404
curl -fsS "${json_header[@]}" -H "Authorization: Bearer $admin_token" \
  -H "Idempotency-Key: $(new_key)" -H "If-Match: \"$version\"" -X POST \
  "$api_base/api/v1/admin/articles/$article_id/restore" --data '{}' >/dev/null

printf '6/7 管理员新增 Source、暂停/恢复并手动抓取\n'
source="$(curl -fsS "${json_header[@]}" -H "Authorization: Bearer $admin_token" \
  -H "Idempotency-Key: $(new_key)" -X POST "$api_base/api/v1/admin/sources" \
  --data "$(jq -cn --arg url "$I2_E2E_FEED_URL" '{feed_url:$url,fetch_interval_seconds:1800}')")"
source_id="$(jq -er '.id' <<<"$source")"
source_version="$(jq -er '.lock_version' <<<"$source")"
paused="$(curl -fsS "${json_header[@]}" -H "Authorization: Bearer $admin_token" \
  -H "Idempotency-Key: $(new_key)" -H "If-Match: \"$source_version\"" -X POST \
  "$api_base/api/v1/admin/sources/$source_id/pause" --data '{}')"
source_version="$(jq -er '.lock_version' <<<"$paused")"
curl -fsS "${json_header[@]}" -H "Authorization: Bearer $admin_token" \
  -H "Idempotency-Key: $(new_key)" -H "If-Match: \"$source_version\"" -X POST \
  "$api_base/api/v1/admin/sources/$source_id/resume" --data '{}' >/dev/null
run="$(curl -fsS "${json_header[@]}" -H "Authorization: Bearer $admin_token" \
  -H "Idempotency-Key: $(new_key)" -X POST "$api_base/api/v1/admin/sources/$source_id/fetches" --data '{}')"
jq -e '.source_id == '"$source_id"' and (.status == "succeeded" or .status == "running")' <<<"$run" >/dev/null

printf '7/7 验证 RSS 与用户投稿同时出现在匿名 latest\n'
latest="$(curl -fsS "$api_base/api/v1/articles?limit=50")"
jq -e --argjson id "$article_id" 'any(.items[]; .id == $id and .origin.type == "user")' <<<"$latest" >/dev/null
jq -e 'any(.items[]; .origin.type == "rss")' <<<"$latest" >/dev/null
printf 'I2 内容供给闭环验证通过：article_id=%s source_id=%s\n' "$article_id" "$source_id"
