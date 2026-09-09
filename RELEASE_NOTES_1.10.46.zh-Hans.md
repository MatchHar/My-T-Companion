# My T Companion 1.10.46

- 修复 HostBox／命令行升级 1.10.45 失败。安装程序现在会把
  `internal/friendtogether` 拷进 Docker 构建目录，不再出现
  `"/internal": not found`。
- 朋友同行仍默认关闭。常规升级不改变停车、导航、配对、APNs 与原有能力。
- 1.10.45 构建失败时已自动回滚；本版是从该回滚继续升级的路径。

不需要数据库、Tesla 令牌或已有数据迁移。
