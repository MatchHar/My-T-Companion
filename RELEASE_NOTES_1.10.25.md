# My T Companion 1.10.25

- Fixes the 1.10.24 Docker build: `push_subscribers.go` was not copied into
  the image, so `update.sh` failed at `go build` and rolled back.
- Dockerfile now copies every `*.go` file. Install and release checks fail if
  a production source would be left out.
- Includes 1.10.24 per-iPhone push subscribers. Do not install 1.10.24.

```bash
sudo MY_T_VERSION=1.10.25 /opt/my-t-companion/update.sh
```
