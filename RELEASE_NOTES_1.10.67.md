# My T Companion 1.10.67

Restores battery display from the latest valid original TeslaMate observation when the live summary is missing or contains a damaged battery reading. The owner-authenticated companion-status endpoint can opt in with `include=battery_observation`; it returns percentage, usable percentage, paired ranges, source and original observation time from one saved positions or charges record. The database read has a two-second timeout and ordinary status requests do not scan history.

Requires a compatible My T client (planned 8.06 build 828); publication of this server release does not mean the App build is already available. Original observations remain historical and are not re-dated or inferred from range. Read failures remain failures so the client can retain its last successful snapshot. Genuine zero battery values and the false low-battery notification protection from 1.10.66 are preserved.

No vehicle wake requests, telemetry writes, database migrations, pairing or notification preference changes. Upgrade through the verified updater or separately signed HostBox catalog; retain the previous verified archive for rollback. Local acceptance: 746 Go tests including actual isolated PostgreSQL 18 fixtures, race and vet; 372 targeted iOS tests. Native device testing and real APNs delivery are not implied by those results. Builds with Go 1.26.11.
