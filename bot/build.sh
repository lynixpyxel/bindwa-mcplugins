#!/bin/bash
set -e

echo "Building static bot-app binary for Linux (Pterodactyl/Docker compatible)..."
CGO_ENABLED=0 go build -ldflags="-s -w -extldflags '-static'" -o bot-app .
echo "Build complete! Binary info:"
file bot-app
