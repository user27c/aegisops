#!/usr/bin/env bash
# 在单台临时 ECS 上运行一次 Cloud Assistant 命令，并等待退出结果。
set -Eeuo pipefail
IFS=$'\n\t'

REGION=""
INSTANCE_ID=""
SCRIPT_FILE=""
TIMEOUT=600

usage() {
  cat <<'EOF'
用法: cloud-assistant-run.sh --region <region> --instance-id <id> --script <file> [--timeout <seconds>]

脚本经 Base64 传给 Cloud Assistant，命令不保留；本工具只输出远端 stdout/stderr 与退出状态。
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --region) REGION="$2"; shift 2 ;;
    --instance-id) INSTANCE_ID="$2"; shift 2 ;;
    --script) SCRIPT_FILE="$2"; shift 2 ;;
    --timeout) TIMEOUT="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "未知参数: $1" >&2; exit 2 ;;
  esac
done

[[ -n "$REGION" && -n "$INSTANCE_ID" && -f "$SCRIPT_FILE" ]] || { usage >&2; exit 2; }
[[ "$TIMEOUT" =~ ^[0-9]+$ && "$TIMEOUT" -ge 30 && "$TIMEOUT" -le 3600 ]] || {
  echo "--timeout 必须介于 30 和 3600 秒" >&2
  exit 2
}
command -v aliyun >/dev/null
command -v jq >/dev/null
command -v base64 >/dev/null

encoded="$(base64 -w0 "$SCRIPT_FILE")"
(( ${#encoded} <= 24576 )) || { echo "脚本 Base64 后超过 Cloud Assistant 24 KiB 限制" >&2; exit 2; }

response_file="$(mktemp "${TMPDIR:-/tmp}/aegisops-assistant-response.XXXXXX")"
error_file="$(mktemp "${TMPDIR:-/tmp}/aegisops-assistant-error.XXXXXX")"
trap 'rm -f "$response_file" "$error_file"' EXIT
if ! aliyun ecs RunCommand \
  --RegionId "$REGION" \
  --Type RunShellScript \
  --CommandContent "$encoded" \
  --ContentEncoding Base64 \
  --InstanceId.1 "$INSTANCE_ID" \
  --Timeout "$TIMEOUT" \
  --read-timeout 60 \
  --connect-timeout 30 \
  --KeepCommand false >"$response_file" 2>"$error_file"; then
  echo "Cloud Assistant 下发失败（CLI 详情已脱敏，避免签名查询串进入日志）" >&2
  exit 1
fi
invoke_id="$(jq -er '.InvokeId' "$response_file")"

deadline=$((SECONDS + TIMEOUT + 60))
while (( SECONDS < deadline )); do
  if ! aliyun ecs DescribeInvocationResults \
    --RegionId "$REGION" \
    --InvokeId "$invoke_id" \
    --InstanceId "$INSTANCE_ID" \
    --read-timeout 60 \
    --connect-timeout 30 \
    --ContentEncoding PlainText >"$response_file" 2>"$error_file"; then
    echo "Cloud Assistant 查询失败（CLI 详情已脱敏）" >&2
    sleep 3
    continue
  fi
  result="$(<"$response_file")"
  status="$(jq -r '.Invocation.InvocationResults.InvocationResult[0].InvokeRecordStatus // .InvocationResults.InvocationResult[0].InvokeRecordStatus // "Pending"' <<<"$result")"
  case "$status" in
    Success|Finished|Failed|Stopped)
      jq -r '.Invocation.InvocationResults.InvocationResult[0].Output // .InvocationResults.InvocationResult[0].Output // ""' <<<"$result"
      exit_code="$(jq -r '.Invocation.InvocationResults.InvocationResult[0].ExitCode // .InvocationResults.InvocationResult[0].ExitCode // 1' <<<"$result")"
      [[ "$exit_code" == "0" && ( "$status" == "Success" || "$status" == "Finished" ) ]] && exit 0
      echo "Cloud Assistant 失败: status=$status exit=$exit_code" >&2
      exit 1
      ;;
  esac
  sleep 3
done

echo "Cloud Assistant 等待超时: invoke_id=$invoke_id" >&2
exit 1
