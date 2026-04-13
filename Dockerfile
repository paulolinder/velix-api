# ─────────────────────────────────────────────────────────────
# Stage 1 — Builder
# ─────────────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w -X main.version=${VERSION}" \
    -o /app/bin/velix-api ./cmd/api

# ─────────────────────────────────────────────────────────────
# Stage 2 — Runtime
# ─────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/bin/velix-api /velix-api

VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["/velix-api"]
