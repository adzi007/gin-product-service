# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/main.go

FROM alpine:3.23
RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=builder /out/app /app/app
USER app

ENV APP_ENV=production
EXPOSE 5000

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:5000/healthz || exit 1

ENTRYPOINT ["/app/app"]
