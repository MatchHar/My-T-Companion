# My T Companion 1.10.13

## 變更

- **可選「鎖車安心」推送**：車輛已上鎖且車內無人時，可透過既有 APNs 中繼發送
  `vehicle_lock_secure` 通知。預設關閉，需完成推送配對後由 App 開啟。
- 新增偏好介面：`GET/PUT /api/v1/notifications/lock-secure`（鑑權與其它
  Companion 介面相同；統一入口既有的 `/api/v1/notifications/*` 即可涵蓋）。
- capabilities 增加 `lock_secure_push`；舊版 My T 不認識該標誌時會忽略。
- 可選自訂提示音名（App 內建 `.caf` 白名單 + `default`）。

## 相容性（重要）

- **不影響既有 My T 版本使用。** 停車、導航、充電即時活動、軟體更新推送及
  既有 API 均不變。
- `app_compatibility.minimum_version` 仍為 **3.10**；
  `recommended_version` 仍為 **3.30**（不會強制升級 App）。
- 新介面與能力均為**增量**；舊版 App 繼續使用同一 base_url 與 Token。
- 鎖車安心預設關閉，僅相容的 My T 在伺服器確認後才會開啟。

## 升級

```sh
sudo /opt/my-t-companion/update.sh
# 或
sudo MY_T_VERSION=1.10.13 /opt/my-t-companion/update.sh
```

TeslaMate 資料與配對狀態保留，無資料庫遷移。

## 回滾

```sh
sudo MY_T_VERSION=1.10.12 /opt/my-t-companion/update.sh
```
