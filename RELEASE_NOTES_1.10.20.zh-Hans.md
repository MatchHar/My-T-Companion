# My T Companion 1.10.20

修复 Tunnel VPS 上升级失败：上次 Edge 把 TeslaMateAPI 留在本机 `:18081` 时，官方升级会把 18081 当成公网入口再套一层，capabilities 检查 404。

- 不再把残留的 `:18081` 当作公网 API 端口。
- 公网 Edge 检查失败时恢复 compose 并继续，Tunnel 机可在 `:8083` 完成验收。
- 若 `:8083` 已能返回 capabilities，跳过再做 8081 Edge。
- 无 API / 数据迁移。

```bash
sudo MY_T_VERSION=1.10.20 /opt/my-t-companion/update.sh
```
