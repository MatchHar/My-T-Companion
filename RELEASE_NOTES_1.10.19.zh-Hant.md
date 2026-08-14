# My T Companion 1.10.19

此版本修正伺服器中已儲存的 MQTT 設定與實際 Docker 拓撲不一致時的升級失敗。

- 當 Mosquitto 實際為 Docker Compose 服務，且主機 1883 連接埠沒有監聽時，
  忽略殘留的 `host.docker.internal` MQTT 位址。
- 自動改用實際運行的 Docker Mosquitto 服務及 TeslaMate 共用網路。
- 繼續保留使用者明確設定的外部 MQTT 主機及真實的主機 Mosquitto。
- 保留校驗和驗證、升級前備份、失敗自動回復及完整的 HostBox／Companion
  就緒驗收。
- 不改變 API 或已儲存資料，繼續相容舊版 My T。

更新現有安裝：

```bash
sudo MY_T_VERSION=1.10.19 /opt/my-t-companion/update.sh
```
