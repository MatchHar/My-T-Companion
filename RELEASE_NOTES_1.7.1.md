# My T Companion 1.7.1

This corrective release makes charging and destination-navigation Live
Activities recover reliably when iOS supplies its push token after the
TeslaMate event has already started.

## Fixed

- Charging now waits for genuine current battery and rated-range values before
  push-to-start.
- An active session whose token was previously unavailable retries with the
  newest complete snapshot.
- A successful push-to-start is immediately followed by the newest complete
  charging or navigation snapshot.
- Navigation retains and sends the last change inside its 15-second coalescing
  window.
- Delayed delivery keeps TeslaMate observation ordering and never fabricates
  missing values.

## Upgrade

```sh
sudo MY_T_VERSION=1.7.1 /opt/my-t-companion/update.sh
```
