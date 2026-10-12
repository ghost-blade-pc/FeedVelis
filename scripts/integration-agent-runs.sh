#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
for name in VELIS_TEST_DATABASE_URL VELIS_TEST_REDIS_ADDRESS VELIS_TEST_OPENSEARCH_URL VELIS_TEST_NGINX_IMAGE; do
  if [[ -z "${!name:-}" ]]; then
    echo "未设置 $name；Agent 回合联合验收不能跳过真实依赖。" >&2
    exit 2
  fi
done
command -v docker >/dev/null
docker image inspect "$VELIS_TEST_NGINX_IMAGE" >/dev/null
artifact_dir="${VELIS_AGENT_TEST_ARTIFACT_DIR:-$(mktemp -d /tmp/velis-agent-runs-XXXXXX)}"
mkdir -p "$artifact_dir"
export GOCACHE="${GOCACHE_DIR:-/tmp/feedvelis-go-cache}"
run_suite() {
  local suite="$1"
  shift
  local log="$artifact_dir/$suite.json"
  if ! (cd backend && go test -json -count=1 -race "$@") >"$log"; then
    python3 scripts/check-go-test-json.py "$log" || true
    echo "验收失败，完整记录：$log" >&2
    exit 1
  fi
  python3 scripts/check-go-test-json.py "$log"
}
run_suite integration ./test/integration -run '^TestAgentRun|^TestAgentEvidence|^TestAgentConversation|^TestAgentTools'
run_suite protocol ./internal/infrastructure/ai/eino ./internal/interfaces/http/hertz/... ./internal/infrastructure/observability ./test/contract -run '^TestReading|^TestAgentRun|^TestAgentObserv'
echo "Agent 回合验收记录：$artifact_dir"
