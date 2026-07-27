# Support

Before opening an issue:

1. Confirm TeslaMate itself is healthy and still recording the expected car.
2. Confirm the ordinary TeslaMateAPI connection works in My T.
3. Run `curl --fail http://127.0.0.1:8083/api/healthz` on the host.
4. Confirm the unified API address routes `/api/v1/capabilities` to the
   companion and requires authentication.
5. Record the My T, companion, TeslaMate, Docker, and reverse-proxy versions.

Include redacted error text and reproduction steps. Never post `.env` files,
tokens, cookies, database passwords, public server addresses, VINs, exact
locations, database exports, or raw production logs.

Use a normal GitHub issue for reproducible functional bugs and documentation
problems. Use GitHub private vulnerability reporting for security issues.
TeslaMate and TeslaMateAPI problems should be reported to their respective
upstream projects after confirming the issue is not caused by this companion.

The component returns only data TeslaMate recorded. Missing historical
sleep/wake or battery observations cannot be reconstructed after the fact.
