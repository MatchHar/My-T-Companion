# 数据生命周期

[English](DATA_LIFECYCLE.md) · 简体中文 · [繁體中文](DATA_LIFECYCLE.zh-Hant.md)

My T Companion 将长期证据与可替换的运行状态分开，并且绝不会修改或删除
TeslaMate PostgreSQL 历史。

| 数据 | 用途 | 默认生命周期 |
|---|---|---|
| 停车插枪、充电、安全及空调变化 | 重建长时间停车记录 | 长期；最新 50,000 条 |
| 软件更新通知 ID | 防止重复提醒 | 180 天；最新 1,000 条 |
| 充电实时活动投递 ID | 防止重复远程更新 | 14 天；最新 2,000 条 |
| 导航实时活动投递 ID | 防止重复远程更新 | 7 天；最新 2,000 条 |
| 活跃充电快照 | 恢复短暂中断的投递 | 48 小时 |
| 活跃导航快照、起点与路径点 | 仅当前行程 | 12 小时 |
| 推送配对 | 私有的设备至中继配置 | 直到取消配对或替换 |
| 待重试推送 | 失败投递所需的最小签名事件 | 按事件类型 10 分钟至 24 小时；总计最新 256 条 |

导航看门程序每 15 分钟检查一次活跃会话。超过 12 小时的会话会通过正常的结束事件路径关闭，
确保锁屏实时活动被结束，而不是留下永久快照。

`PARKING_EVENT_RETENTION_DAYS=0` 表示默认长期政策。管理员可改为 30–3650 天。
`PARKING_EVENT_MAX_EVENTS` 默认为 50,000，可设置为 1,000–500,000；容量清理始终先删除最旧的有效观察。

## 备份与迁移

```sh
sudo /opt/my-t-companion/backup.sh
sudo /opt/my-t-companion/restore.sh /var/backups/my-t-companion/BACKUP.tar.gz
sudo /opt/my-t-companion/storage-status.sh
```

VPS 管理员负责静态加密。备份文件权限为 `0600`、带校验和，并只保留最新 12 份。
默认备份包含长期停车证据和软件通知去重数据，不包含临时充电/导航状态及推送配对密钥；
只有可信迁移才使用 `backup.sh --include-pairing`。

待重试推送只存于用户 VPS 的 Companion 数据卷及同一 `0600` 配对存储中。它不是运营审计日志，
会在成功、暂停、取消配对或过期后删除；除非可信迁移明确包含配对数据，否则不会进入备份。

这不是 TeslaMate 备份。TeslaMate PostgreSQL 必须按 TeslaMate 官方流程独立备份与恢复。
