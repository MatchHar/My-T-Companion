# My T Companion 1.10.24

Push subscribers: one iPhone is one `installation_id`. Switching TeslaMate
servers pauses this phone on the previous VPS instead of wiping everyone.
Coming back resumes the same row. Each phone can choose software-update,
lock-secure, charging Lock Screen, and destination-trip Live Activity, plus
which cars.

Legacy `POST /pair` still upserts. `DELETE /pair` without an installation
header is rejected when more than one phone is registered.

Backward compatible. `recommended_version` remains 3.30.
