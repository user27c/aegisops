# 本地控制台验收证据

- 截图：[本地 Kind 控制台空状态](screenshots/local-e2e-console-20260811.png)
  - Alt：深色 AegisOps 事故控制台；认证后的事故统计、筛选器和“暂无事故”状态。
  - 采集时间：2026-08-11 21:41 CST；基础 Git SHA：`dda163402ed1`（采集包含尚未提交的本地修复）。
  - 环境：`kind-aegisops-e2e`；受限 viewer token 只存于本次浏览器 sessionStorage，未写入截图、视频、日志或仓库。
  - 验证：`GET /api/v1/incidents` 返回 200、0 条事故；桌面和 390px 响应式视口均视觉确认。
- 视频：[本地控制台短视频](videos/local-e2e-console-20260811.webm)
  - 同次受控浏览器会话录制，4.44 秒；包含命名空间/严重级别筛选交互与空状态，不包含 token 或事故证据正文。

这些是本地验收材料，不得作为云端 smoke 或模型质量的替代证据。
