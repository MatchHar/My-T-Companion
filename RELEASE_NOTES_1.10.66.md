# My T Companion 1.10.66

Prevents false low-battery alerts caused by garbled TeslaMate legacy stream frames. Invalid gear context or a battery percentage inconsistent with usable SOC remains unconfirmed; it cannot create/rearm an episode or consume a pending reminder. Genuine low and zero battery readings remain supported. Durable low-battery retries recheck the current car, episode and battery context before sending. No raw telemetry or vehicle settings are changed.

Builds with Go 1.26.11, including the upstream net/http fix for GO-2026-6859.
