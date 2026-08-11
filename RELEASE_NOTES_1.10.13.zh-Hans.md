# My T Companion 1.10.13

## 变更

- **可选「锁车安心」推送**：车辆已上锁且车内无人时，可通过既有 APNs 中继发送
  `vehicle_lock_secure` 通知。默认关闭，需完成推送配对后由 App 开启。
- 新增偏好接口：`GET/PUT /api/v1/notifications/lock-secure`（鉴权与其它
  Companion 接口相同；统一入口已有的 `/api/v1/notifications/*` 即可覆盖）。
- capabilities 增加 `lock_secure_push`；旧版 My T 不认识该标志时会忽略。
- 可选自定义提示音名（App 内置 `.caf` 白名单 + `default`）。

## 兼容性（重要）

- **不影响既有 My T 版本使用。** 停车、导航、充电实时活动、软件更新推送及
  既有 API 均不变。
- `app_compatibility.minimum_version` 仍为 **3.10**；
  `recommended_version` 仍为 **3.30**（不会强制升级 App）。
- 新接口与能力均为**增量**；旧版 App 继续使用同一 base_url 与 Token。
- 锁车安心默认关闭，仅兼容的 My T 在服务器确认后才会开启。

## 升级

```sh
sudo /opt/my-t-companion/update.sh
# 或
sudo MY_T_VERSION=1.10.13 /opt/my-t-companion/update.sh
```

TeslaMate 数据与配对状态保留，无数据库迁移。

## 回滚

```sh
sudo MY_T_VERSION=1.10.12 /opt/my-t-companion/update.sh
```
