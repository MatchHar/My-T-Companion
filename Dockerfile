FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# VERSION is embedded into the binary (//go:embed) — keep in sync with release tags.
# List production sources explicitly so a missed install-copy cannot silently omit a file
# (Docker `COPY *.go` only builds whatever is present; missing lock_secure caused undefined types).
COPY VERSION ./
COPY main.go notification.go charging_notification.go navigation_notification.go \
     parking_event_monitor.go storage_policy.go lock_secure_notification.go ./
RUN test -f lock_secure_notification.go \
  && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/states-api .

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S -g 10001 myt && adduser -S -D -H -u 10001 -G myt myt
RUN install -d -o 10001 -g 10001 -m 0700 /data
WORKDIR /app
COPY --from=build /out/states-api /app/states-api
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/states-api"]
