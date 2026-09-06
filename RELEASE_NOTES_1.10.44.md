# My T Companion 1.10.44

- Keep the last confirmed destination when Tesla briefly reports an older,
  far-away destination after arrival without usable route distance or time.
- Discard that staged terminal value when parking or route-clear follows, so it
  cannot create a false leg or arrival notification.
- Confirm a real nearby next stop only after the same candidate persists while
  the vehicle remains authoritatively driving.
- Clear all previous route metrics before committing a genuine redirect, so a
  partial observation cannot inherit an old leg's zero distance, zero minutes,
  destination coordinates, or arrival battery.
- Preserve the existing API, pairings, per-vehicle notification preferences,
  navigation history, polling rate, and vehicle wake behavior.

No database, proxy, Tesla token, or stored-data migration is required.
