# ---- Builder ----
FROM golang:1.26.1-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=0.6.0
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/costa/polypod/internal/autoupdate.currentVersion=${VERSION}" -o polypod .

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
COPY --from=builder /app/agents /home/polypod/agents
COPY --from=builder /app/templates /home/polypod/templates
COPY --from=builder /app/config.example.yaml /etc/polypod/config.yaml

USER polypod
WORKDIR /home/polypod

ENTRYPOINT ["polypod"]
CMD ["/etc/polypod/config.yaml"]
