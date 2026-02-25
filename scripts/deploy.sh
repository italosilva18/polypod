#!/bin/bash
set -euo pipefail

# Polypod deploy script
# Usage: ./scripts/deploy.sh <server> [user]

SERVER="${1:?Usage: deploy.sh <server> [user]}"
USER="${2:-polypod}"
REMOTE_DIR="/opt/polypod"
BINARY="polypod"
SERVICE="polypod"

echo "==> Building for Linux..."
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$BINARY" .

echo "==> Deploying to $SERVER..."
rsync -avz --progress \
    "$BINARY" \
    config.yaml \
    "$USER@$SERVER:$REMOTE_DIR/"

echo "==> Restarting service..."
ssh "$USER@$SERVER" "sudo systemctl restart $SERVICE"

echo "==> Checking status..."
ssh "$USER@$SERVER" "sudo systemctl status $SERVICE --no-pager"

echo "==> Deploy complete!"
rm -f "$BINARY"
