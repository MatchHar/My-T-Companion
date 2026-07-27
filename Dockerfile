FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/states-api .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S -g 10001 myt && adduser -S -D -H -u 10001 -G myt myt
WORKDIR /app
COPY --from=build /out/states-api /app/states-api
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/states-api"]
