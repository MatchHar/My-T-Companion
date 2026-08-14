# My T Companion 1.10.20

修復 Tunnel VPS 上升級失敗：上次 Edge 把 TeslaMateAPI 留在本機 `:18081` 時，官方升級會把 18081 當成公網入口再套一層，capabilities 檢查 404。

- 不再把殘留的 `:18081` 當作公網 API 埠。
- 公網 Edge 檢查失敗時恢復 compose 並繼續，Tunnel 機可在 `:8083` 完成驗收。
- 若 `:8083` 已能回 capabilities，略過再做 8081 Edge。
- 無 API / 資料遷移。

```bash
sudo MY_T_VERSION=1.10.20 /opt/my-t-companion/update.sh
```
