#!/bin/bash
set -euo pipefail

# Builds doxie-scanner, seeds it with synthetic demo data (no real scanned
# documents involved — see generate-seed-data.py), runs the Playwright
# walkthrough in demo.spec.js against it, and converts the resulting
# video into a palette-optimized GIF at output/doxie-scanner-demo.gif.
#
# demo.spec.js is both the integration test and the demo recording: a
# failing assertion fails this script exactly like it would fail CI.
#
# This is the web-app equivalent of vscode-git-log-viewer/demo: that one
# drives a real desktop VS Code window with xdotool/wmctrl; there's no
# desktop window here, so Playwright drives a real browser instead,
# inside the official Playwright Docker image — the host has neither
# Node nor browsers installed, and doesn't need them for this.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

PLAYWRIGHT_IMAGE="mcr.microsoft.com/playwright:v1.61.0-jammy"
APP_IMAGE="doxie-scanner:demo"
NETWORK="doxie-demo-net"
APP_CONTAINER="doxie-demo-app"

WORK_DIR="$(mktemp -d)"
DATA_DIR="$WORK_DIR/data"
OUTPUT_DIR="$SCRIPT_DIR/output"

GIF_FPS=8
GIF_WIDTH=900
GIF_MAX_COLORS=160

cleanup() {
    echo "Cleaning up..."
    docker rm -f "$APP_CONTAINER" >/dev/null 2>&1 || true
    docker network rm "$NETWORK" >/dev/null 2>&1 || true
    rm -rf "$WORK_DIR"
}
trap cleanup EXIT

mkdir -p "$DATA_DIR" "$OUTPUT_DIR"

echo "Generating synthetic seed data..."
python3 "$SCRIPT_DIR/generate-seed-data.py" "$DATA_DIR"

echo "Building doxie-scanner image..."
docker build -t "$APP_IMAGE" "$REPO_ROOT"

echo "Setting up an isolated network..."
docker network rm "$NETWORK" >/dev/null 2>&1 || true
docker network create "$NETWORK" >/dev/null

echo "Starting doxie-scanner (no scanner device attached — deliberately, so the demo shows the real 'not connected' state)..."
docker rm -f "$APP_CONTAINER" >/dev/null 2>&1 || true
docker run -d --name "$APP_CONTAINER" --network "$NETWORK" \
    -v "$DATA_DIR:/data" \
    "$APP_IMAGE" >/dev/null

echo "Waiting for doxie-scanner to become healthy..."
ready=""
for _ in $(seq 1 30); do
    if docker exec "$APP_CONTAINER" wget -q -O- "http://localhost:8080/api/version" >/dev/null 2>&1; then
        ready=1
        break
    fi
    sleep 1
done
if [[ -z "$ready" ]]; then
    echo "doxie-scanner never became healthy — check 'docker logs $APP_CONTAINER'" >&2
    exit 1
fi

rm -rf "$SCRIPT_DIR/test-results"

echo "Running the Playwright walkthrough (this both tests and records)..."
docker run --rm \
    --network "$NETWORK" \
    --user "$(id -u):$(id -g)" \
    -e HOME=/tmp \
    -e DOXIE_BASE_URL="http://$APP_CONTAINER:8080" \
    -v "$SCRIPT_DIR:/work" \
    -w /work \
    "$PLAYWRIGHT_IMAGE" \
    bash -c "npm install --no-audit --no-fund --no-package-lock && npx playwright test"

VIDEO="$(find "$SCRIPT_DIR/test-results" -name '*.webm' | head -1)"
if [[ -z "$VIDEO" ]]; then
    echo "No recorded video found under test-results/ — did the test run?" >&2
    exit 1
fi

echo "Converting $VIDEO to GIF..."
PALETTE="$WORK_DIR/palette.png"
ffmpeg -y -i "$VIDEO" \
    -vf "fps=${GIF_FPS},scale=${GIF_WIDTH}:-1:flags=lanczos,palettegen=max_colors=${GIF_MAX_COLORS}:stats_mode=diff" \
    "$PALETTE" >"$WORK_DIR/palette.log" 2>&1
ffmpeg -y -i "$VIDEO" -i "$PALETTE" \
    -lavfi "fps=${GIF_FPS},scale=${GIF_WIDTH}:-1:flags=lanczos [x]; [x][1:v] paletteuse=dither=bayer:bayer_scale=3" \
    "$OUTPUT_DIR/doxie-scanner-demo.gif" >"$WORK_DIR/gif.log" 2>&1

echo "Done: $OUTPUT_DIR/doxie-scanner-demo.gif"
