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
RUN apk add --no-cache \
      iputils \
      bind-tools \
      ca-certificates \
      curl \
    && adduser -D -H -u 10001 lg \
    && arch=$(uname -m) \
    && case "$arch" in x86_64) nt=amd64 ;; aarch64) nt=arm64 ;; *) echo "unsupported arch: $arch"; exit 1 ;; esac \
    && curl -fsSL "https://github.com/nxtrace/NTrace-core/releases/download/${NEXTTRACE_VERSION}/nexttrace_linux_${nt}" \
         -o /usr/local/bin/nexttrace \
    && chmod +x /usr/local/bin/nexttrace
WORKDIR /app
COPY --from=build /out/next-looking-glass /app/next-looking-glass
# nexttrace needs raw sockets; run as root in container (or use cap-add NET_RAW).
EXPOSE 8080
ENTRYPOINT ["/app/next-looking-glass"]
CMD ["-config", "/app/config.yaml"]
