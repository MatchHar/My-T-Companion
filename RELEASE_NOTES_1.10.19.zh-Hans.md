# My T Companion 1.10.19

此版本修正服务器中已保存的 MQTT 设置与实际 Docker 拓扑不一致时的升级失败。

- 当 Mosquitto 实际为 Docker Compose 服务，且宿主机 1883 端口没有监听时，
  忽略残留的 `host.docker.internal` MQTT 地址。
- 自动改用实际运行的 Docker Mosquitto 服务及 TeslaMate 共用网络。
- 继续保留用户明确配置的外部 MQTT 主机及真实的宿主机 Mosquitto。
- 保留校验和验证、升级前备份、失败自动回滚及完整的 HostBox／Companion
  就绪验收。
- 不改变 API 或已存数据，继续兼容旧版 My T。

更新现有安装：

```bash
sudo MY_T_VERSION=1.10.19 /opt/my-t-companion/update.sh
```
