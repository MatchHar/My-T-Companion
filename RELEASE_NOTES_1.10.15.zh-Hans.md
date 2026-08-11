# My T Companion 1.10.15

## 修复（HostBox / 更新编译）

- Dockerfile **显式列出**生产用 Go 源文件（含 `lock_secure_notification.go`）。
- 安装前检查缺文件会直接失败，并打印构建目录文件列表。
- 使用 `docker compose build --progress=plain`，方便在 HostBox 弹窗看到完整编译错误。
- 运行内存上限调至 **512m**。

含 1.10.13–1.10.14 锁车安心功能；**不影响**旧版 My T。

## 升级

```sh
sudo MY_T_VERSION=1.10.15 /opt/my-t-companion/update.sh
```

或 HostBox → 组件版本 → 更新到 **1.10.15**。
