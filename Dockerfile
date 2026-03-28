# ---- Builder ----
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/costa/polypod/internal/autoupdate.currentVersion=0.4.0" -o polypod .

# ---- Runtime ----
FROM alpine:3.20

RUN apk add --no-cache \
    ca-certificates \
    git \
    bash \
    curl \
    openssh-client \
    && adduser -D -h /home/polypod polypod

COPY --from=builder /app/polypod /usr/local/bin/polypod
COPY --from=builder /app/agents /etc/polypod/agents
COPY --from=builder /app/templates /etc/polypod/templates
COPY --from=builder /app/config.example.yaml /etc/polypod/config.example.yaml

USER polypod
WORKDIR /home/polypod

EXPOSE 8080 8090

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD curl -sf http://localhost:8080/api/health || exit 1

ENTRYPOINT ["polypod"]
CMD ["/etc/polypod/config.yaml"]
