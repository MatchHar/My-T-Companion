# My T Companion 1.10.10

- Fixes incorrect `capabilities.version` / `API-Version` still reporting **1.10.8**
  after upgrading to the 1.10.9 package (hardcoded constant vs VERSION file).
- Version is now embedded from the release `VERSION` file.
- Optional `teslamate_version` on capabilities (from TeslaMate container image tag)
  so My T can display TeslaMate version without relying on web scrape of port 4000.
