# 真实 SMTP 邮件证据（v0.2.0，脱敏）

- 目标：`smtp.qq.com:587`（STARTTLS，`requireTLS: true`）
- 发件 = 收件，均脱敏为 `<redacted>@qq.com`（原始 `.omo/evidence/task-6-*` 记录为 `x***7@qq.com`）
- 唯一告警名：`AegisOpsEmailSmokeTest`（FIRING 与 RESOLVED 各发 1 封，共 2 封）
- 镜像：`prom/alertmanager:v0.27.0`

## 投递证据

Alertmanager debug 日志两条 `msg="Notify success"`（真实 SMTP 往返约 1.2s）：

```text
... alerts=[AegisOpsEmailSmokeTest[6990dfd][active]]   msg="Notify success" attempts=1 duration=1.216s
... alerts=[AegisOpsEmailSmokeTest[6990dfd][resolved]] msg="Notify success" attempts=1 duration=1.193s
```

指标佐证：

```text
alertmanager_notifications_total{integration="email"} 2
alertmanager_notifications_failed_total{integration="email",reason="<any>"} 0
```

## 门禁断言

```bash
python3 scripts/assert-test-email.py --real-smtp --alertmanager-url http://127.0.0.1:19094 \
  --expect-min-delivered 2 --settle 40 --timeout 120
# → OK 真实 SMTP 投递: delivered=2 (期望>=2) total=2 failed=0；exit=0
```

## MailHog 路径（inhibition / repeat / group_wait）

`make test-alerting` exit 0：critical 抑制同组 warning、`no_new_message` 覆盖 repeat-interval/group_wait。

## 脱敏说明

SMTP 授权码仅存在于 0600 的 `.local/secrets/smtp-password`（gitignored），从未回显；
收件邮箱、Message-ID 未入库。
