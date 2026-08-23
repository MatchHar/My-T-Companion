# My T Companion 1.10.29

## 修正 TeslaMate 版本显示

- Companion 现在从 TeslaMate 内部设置页面实时读取当前版本。TeslaMate
  升级后，安装时保存的旧 `TESLAMATE_VERSION` 不会再覆盖实时结果。
- 能力接口新增 `teslamate_version_source` 与
  `teslamate_version_checked_at`，My T 可据此区分实时数据和安装信息回退值。
- 安装与更新会优先从正在运行的 TeslaMate 容器取得回退版本，最后才读取旧配置。

## 发布与交付加固

- 安装、Docker 构建及发布完整性检查均已包含新的版本探测文件。
- 受信任的部署工具可使用独立签名的 HostBox 目录摘要再次校验发布压缩包。
- 持续超过 12 小时的遗留导航会话现在会按正常结束事件关闭。
- 版本标签包含漏洞检查、可重复生成的压缩包、校验值及构建来源证明。

无需迁移数据库，也无需重新配对推送。
