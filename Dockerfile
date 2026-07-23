# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.26-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/next-looking-glass .

# ---- runtime stage ----
FROM alpine:3.21
ARG NEXTTRACE_VERSION=v1.7.1
ARG UNLOCK_TEST_VERSION=latest
RUN apk add --no-cache \
      iputils \
      bind-tools \
      ca-certificates \
      curl \
    && adduser -D -H -u 10001 lg \
    && arch=$(uname -m) \
    && case "$arch" in \
         x86_64)  arch_=amd64 ;; \
         aarch64) arch_=arm64 ;; \
         *) echo "unsupported arch: $arch"; exit 1 ;; \
       esac \
    # nexttrace — routing + geo data
    && curl -fsSL "https://github.com/nxtrace/NTrace-core/releases/download/${NEXTTRACE_VERSION}/nexttrace_linux_${arch_}" \
         -o /usr/local/bin/nexttrace \
    && chmod +x /usr/local/bin/nexttrace \
    # unlock-test — streaming-region checks (shelled out from /api/unlock)
    && curl -fsSL "https://github.com/HsukqiLee/MediaUnlockTest/releases/${UNLOCK_TEST_VERSION}/download/unlock-test_linux_${arch_}" \
         -o /usr/local/bin/unlock-test \
    && chmod +x /usr/local/bin/unlock-test
WORKDIR /app
COPY --from=build /out/next-looking-glass /app/next-looking-glass
# nexttrace needs raw sockets; run as root in container (or use cap-add NET_RAW).
EXPOSE 8080
ENTRYPOINT ["/app/next-looking-glass"]
CMD ["-config", "/app/config.yaml"]
