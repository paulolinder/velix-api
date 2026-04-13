# ─────────────────────────────────────────────────────────────
# Stage 1 — Builder
# ─────────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

ARG VERSION=dev

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /app/bin/velix-api ./cmd/api

# ─────────────────────────────────────────────────────────────
# Stage 2 — Runtime
# ─────────────────────────────────────────────────────────────
FROM alpine:3

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/bin/velix-api /velix-api

VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["/velix-api"]
