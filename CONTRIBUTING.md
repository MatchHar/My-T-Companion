# Contributing

Bug reports and focused pull requests are welcome. Never include credentials,
`.env` files, VINs, vehicle locations, database exports, or production logs.

Before opening a pull request:

1. Update or add tests for behavior changes.
2. Run `go test ./...`, `go vet ./...`, and the shell syntax checks documented
   in the README.
3. Keep the PostgreSQL connection read-only and port 8083 localhost-only.
4. Do not add inferred vehicle events or estimated battery/range data.
5. Explain compatibility and rollback impact.

Use GitHub private vulnerability reporting for security issues.
