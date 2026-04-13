# ─────────────────────────────────────────────────────────────
# Stage 1 — Builder
# ─────────────────────────────────────────────────────────────
# Sem --platform=$BUILDPLATFORM: Buildx escolhe a imagem correta
# para cada plataforma alvo. Para arm64, roda via QEMU (já configurado
# no CI). CGO desabilitado = compilação pura Go, rápida mesmo emulada.
FROM golang:1.25-alpine AS builder

ARG VERSION=dev

RUN apk add --no-cache ca-certificates tzdata git

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
