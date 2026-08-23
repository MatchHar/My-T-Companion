# HostBox signed stable catalog

The public `myt-stack.json` is a privileged deployment input used by HostBox.
It is accepted only when `myt-stack.json.sig` verifies with the adjacent Ed25519
public key and every image, port, path, version, and channel field passes the
App's strict allowlist.

Stable catalogs never enable `follow_latest_release`. Upstream releases are
reported separately and enter this catalog only after compatibility, database
migration, upgrade, and rollback validation. Registry images use immutable
`tag@sha256:digest` references. The Companion release archive has a separate
SHA-256 anchored in the signed catalog. HostBox re-verifies cached bytes at
launch and rejects catalog rollback.

The signing private key is kept offline/in the maintainer's macOS Keychain and
is never stored in this repository or GitHub Actions. The canonical authoring
and signing scripts live in the private HostBox repository. Run
`scripts/verify-hostbox-catalog.sh` before publishing this public copy.

简体中文：此目录只发布已验证的固定版本；HostBox 会同时验证签名、镜像白名单与
不可变 digest，并以签名目录中的 SHA-256 核对 Companion 安装包；缓存会重新验签并拒绝降级。
上游最新版不会自动进入稳定部署。

繁體中文：此目錄只發布已驗證的固定版本；HostBox 會同時驗證簽章、映像白名單與
不可變 digest，並以簽章目錄中的 SHA-256 核對 Companion 安裝包；快取會重新驗簽並拒絕降級。
上游最新版本不會自動進入穩定部署。
