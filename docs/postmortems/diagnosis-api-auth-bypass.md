---
run_id: M9.1a-diagnosis-api-auth-bypass
reviewed: false
---

# Postmortem: Diagnosis API 鉴权未接线

## Summary

Diagnosis 服务的全部 `/v1/**` 路由（analyses / audit / runbooks）在 M9.1a 之前未接入任何鉴权。Go 客户端（operator / httpapi）早已发送 `Authorization: Bearer ...`，但 FastAPI 侧从未校验该 Header；`api_token` 配置字段存在却未挂到路由依赖上。任何能到达 Diagnosis API 网络的调用方都能匿名提交诊断、读取审计事件。已通过新增 `app/security.py` 的 `require_service_token` 依赖 + protected 子 Router 修复：`/v1/**` 统一走 Bearer Token 鉴权，`/healthz`、`/readyz` 保持公开。

## Impact

- 影响面：Diagnosis API 的 `/v1/**` 端点对集群内/网络内匿名开放。
- 时长：从 M3 上线（commit `c5656d0`）到 M9.1a 修复（commit `fc881a3`）。
- 严重级别：高危（服务间信任边界缺失）。注：Diagnosis 本身无 Kubernetes 写权限，故该缺陷不直接放大为集群写权限暴露，但破坏了「Operator / Incident API → Diagnosis」的服务间信任契约。

## Timeline

| 时间                           | 事件                                                              |
| ------------------------------ | ----------------------------------------------------------------- |
| M3（commit `c5656d0`）         | Diagnosis API 上线，`api_token` 配置字段已存在但未接入路由        |
| M9.1 规划（NEXT-STEPS §6.1）   | 事实表审计发现「Diagnosis API 鉴权未接线」                        |
| 2026-08-02（commit `fc881a3`） | 新增 security.py + protected Router + Helm Secret，并补 13 个单测 |

## Detection

非运行时告警。由 M9.1 事实表审计（对照 `docs/implementation-status.md` 第 15 行）发现的静态缺陷：Go 客户端已发送 Bearer，而 FastAPI 路由无任何鉴权依赖。

## Evidence

- 事实表第 15 行（`docs/implementation-status.md`）：「Diagnosis API 服务间鉴权」由「未接线」改为 `yes`。
- 代码证据：修复前 `services/diagnosis/app/api/__init__.py` 直接 `include_router(analyses.router)` / `include_router(audit.router)`，无 `dependencies=[Depends(...)]`。
- 修复 commit：`fc881a3`（M9.1a）。

## Root Cause

鉴权职责未接线。`api_token` 配置字段自 M3 起就存在，但 FastAPI 路由从未声明 `Depends(require_service_token)`，`Authorization` Header 被完全忽略。根因是 M3 阶段优先打通诊断链路，鉴权被列为后续项（M9.1），而未在路由层强制落地。

## Contributing Factors

- FastAPI 默认不鉴权：需为每条路由或 Router 显式挂依赖，漏挂不报错。
- 客户端先行发送 Bearer，造成「看起来已鉴权」的假象，掩盖了服务端未校验的事实。

## Why Tests Missed It

- 既有 API 测试用 TestClient 直连路由，均以「无鉴权」为前提编写并通过。
- 缺少负向测试：没有任何用例断言「未带 Token / 错误 scheme / 错误 Token 应返回 401」。

## Corrective Action

- 新增 `services/diagnosis/app/security.py`：
  - `load_api_token`：优先读 `api_token_file`（≤4KiB，strip），其次显式 `api_token`（SecretStr）；空值返回 None。
  - `parse_bearer_header`：只接受 Bearer scheme，缺失/空 Token/其他 scheme 均拒绝。
  - `verify_token`：SHA256 后 `hmac.compare_digest`，避免直接字符串比较。
  - `require_service_token`：统一 401 响应，不泄露「未配置」还是「Token 错误」；未配置 Token 仅 `environment=development` + `allow_insecure_no_auth` 时放行，否则 fail-closed。
- `services/diagnosis/app/api/__init__.py`：health 路由公开；analyses/audit/runbooks 挂到带 `dependencies=[Depends(require_service_token)]` 的 protected 子 Router。
- `services/diagnosis/app/config.py`：新增 `api_token_file`、`allow_insecure_no_auth`、`environment`。
- Helm：`diagnosis-api-deployment.yaml` 挂载 `aegisops-diagnosis-token`（`defaultMode: 0440`）+ `DIAGNOSIS_API_TOKEN_FILE`；`values.schema.json` 校验 Secret 名非空。

## Regression Test

- 测试文件：`services/diagnosis/tests/unit/test_security.py`（15 个用例，真实存在且通过）。
- 命令：`cd services/diagnosis && uv run pytest tests/unit/test_security.py -q`
- 覆盖：无 Header → 401；Basic scheme → 401；空 Bearer Token → 401；错误 Token → 401；正确 Token 进入业务路由（非 401）；Token 前后空白 trim；`/healthz` 无 Token → 200；production 未配置 Token fail-closed；Secret 文件不可读仍统一 401；`DIAGNOSIS_API_TOKEN_FILE` 环境变量映射；认证失败响应不泄露 Token 原文。
- 实测结果：`15 passed`。

## Preventive Control

- 鉴权默认 fail-closed：`/v1/**` 统一挂 `require_service_token` 依赖，新路由必须显式挂到 protected Router，不能直接 `include_router` 到公开 Router。
- 负向测试（401 断言）列入单测基线。
- 事实表逐项审计（`docs/implementation-status.md`）纳入 M9.x 验收，防止「配置存在但未接线」回归。

## Verification

- `cd services/diagnosis && uv run pytest tests/unit/test_security.py -q` → `15 passed`。
- Kind full E2E 鉴权边界通过（`docs/implementation-status.md` 第 15 行标记 `yes`）。

## What Went Well / What Failed

- Well：修复的同时补齐负向测试与统一 401 语义，避免泄露失败原因。
- Failed：鉴权本应在 M3 阶段即接线，而非延后到 M9.1 才作为缺陷修复。

## Action Items

- [ ] 由人工确认并把 frontmatter 改为 `reviewed: true` 后进入 RAG 索引。

## Raw Artifact Links

- 修复 commit：`fc881a3`（M9.1a: Diagnosis API 服务间 Bearer Token 鉴权）。
- 代码：`services/diagnosis/app/security.py`、`services/diagnosis/app/api/__init__.py`、`services/diagnosis/app/config.py`。

> 约束：LLM 生成的草稿必须经人工确认并把 frontmatter 改为 `reviewed: true` 才能进入 RAG 索引。
