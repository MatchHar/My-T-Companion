FROM golang:1.26.6-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# VERSION is embedded into the binary (//go:embed) — keep in sync with release tags.
# List production sources explicitly so a missed install-copy cannot silently omit a file
# (Docker `COPY *.go` only builds whatever is present; missing lock_secure caused undefined types).
COPY VERSION ./
# Copy every Go file. An explicit filename list (1.10.15–1.10.24) omitted
# new production sources such as push_subscribers.go, so `go build` failed
# on VPS while GitHub CI was sometimes bypassed.
COPY *.go ./
RUN test -f lock_secure_notification.go \
  && test -f push_subscribers.go \
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
