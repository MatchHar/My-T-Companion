# My T Companion 1.10.5

- Fix a mutex deadlock that blocked `GET /api/v1/cars/{id}/navigation/push-history` after the first navigation push delivery.
- Ensure Caddy/nginx install snippets and installer route `navigation/push-history` to Companion (not TeslaMate API). Without this reverse-proxy rule the App receives 404 and shows an empty history even when sessions exist on disk.
- Navigation push history is readable again from My T Settings.
