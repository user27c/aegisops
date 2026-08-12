#!/usr/bin/env bash
# 轮换隔离 Kind E2E 的 viewer-only console token。Token 永不写 stdout/stderr；
# 仅更新本次 run 的 Secret、incident-api Pod 和 0600 environment.json。
set -Eeuo pipefail
IFS=$'\n\t'

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$ROOT/.local/e2e/environment.json"
KUBECONFIG_PATH="${AEGISOPS_E2E_KUBECONFIG:-$HOME/.kube/config}"

usage() {
  cat <<'EOF'
用法: rotate-e2e-viewer-token.sh

仅轮换由本项目创建的隔离 Kind E2E 环境中的 viewer-only console token。
需要 .local/e2e/environment.json、.local/secrets/console-tokens 和可用集群。
EOF
}

if [[ $# -gt 0 ]]; then
  case "$1" in
    -h|--help) usage; exit 0 ;;
    *) printf '%s\n' "[ERROR] 未知参数: $1" >&2; usage >&2; exit 2 ;;
  esac
fi

[[ -f "$ENV_FILE" ]] || { printf '%s\n' '[ERROR] 缺少 .local/e2e/environment.json' >&2; exit 1; }
command -v openssl >/dev/null || { printf '%s\n' '[ERROR] 缺少 openssl' >&2; exit 1; }

CONTEXT="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["context"])' "$ENV_FILE")"
NAMESPACE="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["namespace"])' "$ENV_FILE")"
kubectl_cmd=(kubectl --kubeconfig "$KUBECONFIG_PATH" --context "$CONTEXT" -n "$NAMESPACE")
NEW_TOKEN="console-token-$(openssl rand -hex 24)"

# 从本机 0600 配置读取原始 approver/多角色行，追加一枚新的 viewer-only 行。
# 通过 --from-file 写入真实换行，避免 shell/Python 转义再次损坏 token 文件。
LOCAL_TOKENS="$ROOT/.local/secrets/console-tokens"
[[ -f "$LOCAL_TOKENS" ]] || { printf '%s\n' '[ERROR] 缺少 .local/secrets/console-tokens' >&2; exit 1; }
tmp_tokens="$(mktemp)"
chmod 0600 "$tmp_tokens"
trap 'rm -f -- "$tmp_tokens"' EXIT
LOCAL_TOKENS="$LOCAL_TOKENS" NEW_TOKEN="$NEW_TOKEN" python3 - "$tmp_tokens" <<'PY'
import os
import sys
from pathlib import Path

source = Path(os.environ["LOCAL_TOKENS"])
lines = [line for line in source.read_text(encoding="utf-8").splitlines() if line.strip()]
assert lines, "console-tokens 文件为空"
out = lines + [os.environ["NEW_TOKEN"] + ":viewer"]
Path(sys.argv[1]).write_text("\n".join(out) + "\n", encoding="utf-8")
PY

"${kubectl_cmd[@]}" create secret generic aegisops-console-auth \
  --from-file=tokens="$tmp_tokens" --dry-run=client -o yaml \
  | "${kubectl_cmd[@]}" apply -f - >/dev/null
"${kubectl_cmd[@]}" rollout restart deployment/aegisops-incident-api >/dev/null
"${kubectl_cmd[@]}" rollout status deployment/aegisops-incident-api --timeout=180s >/dev/null

NEW_TOKEN="$NEW_TOKEN" python3 - "$ENV_FILE" <<'PY'
import json
import os
import sys
from pathlib import Path

path = Path(sys.argv[1])
value = json.loads(path.read_text(encoding="utf-8"))
value["viewerToken"] = os.environ["NEW_TOKEN"]
temporary = path.with_suffix(path.suffix + ".tmp")
temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
temporary.chmod(0o600)
temporary.replace(path)
path.chmod(0o600)
PY

# 只验证 HTTP 状态，不输出响应体或 token。
status="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' -H "Authorization: Bearer $NEW_TOKEN" "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["incidentApiUrl"])' "$ENV_FILE")/api/v1/incidents")"
[[ "$status" == 200 ]] || { printf '%s\n' "[ERROR] 新 viewer token 验证失败: HTTP $status" >&2; exit 1; }
printf '%s\n' 'isolated E2E viewer token rotated and verified'
