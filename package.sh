#!/usr/bin/env bash
# Builds MonitorSwap.exe and assembles the release zip in ./dist.
# Works on Linux, macOS or Windows (Git Bash); needs Go 1.21+ and zip.
set -euo pipefail
cd "$(dirname "$0")"

go vet ./...
go test ./...

out=dist/MonitorSwap
rm -rf dist
mkdir -p "$out/Desktop" "$out/Laptop"

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "-H=windowsgui -s -w" -o "$out/Desktop/MonitorSwap.exe" .
cp "$out/Desktop/MonitorSwap.exe" "$out/Laptop/MonitorSwap.exe"

# Windows line endings so the text files open cleanly in Notepad.
crlf() { sed 's/\r$//; s/$/\r/' "$1" > "$2"; }
crlf examples/desktop/settings.json "$out/Desktop/settings.json"
crlf examples/laptop/settings.json  "$out/Laptop/settings.json"
crlf packaging/README.txt           "$out/README.txt"

(cd dist && zip -qr MonitorSwap.zip MonitorSwap)
echo "Built dist/MonitorSwap.zip"
