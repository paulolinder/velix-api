# ─────────────────────────────────────────────────────────────
# Stage 1 — Builder
# ─────────────────────────────────────────────────────────────

# Escopo global — Buildx injeta os valores corretos para cada plataforma.
# Devem ficar ANTES do primeiro FROM para não serem sobrescritos pelo
# --platform do builder stage.
ARG TARGETOS=linux
ARG TARGETARCH=amd64

FROM --platform=$BUILDPLATFORM golang:1.25.0-alpine AS builder

# Herda do escopo global (não define default aqui — usa o valor injetado)
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

RUN apk add --no-cache ca-certificates tzdata git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
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
