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

# Must match the viewport/video size in playwright.config.js, and stay in
# the same order as the numbered tests in demo.spec.js — each caption
# becomes the title card shown right before that test's recorded clip.
CLIP_WIDTH=1280
CLIP_HEIGHT=860
CLIP_FPS=10
TITLE_DUR=1.6
FONT="$(fc-match -f '%{file}\n' 'DejaVu Sans:bold' 2>/dev/null || echo /usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf)"
CAPTIONS=(
    "Live scanner connection status"
    "Browse, rotate, and crop a page"
    "Combine pages from multiple scans into one PDF"
    "Rename a scan and delete a page"
)

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

VIDEOS=($(find "$SCRIPT_DIR/test-results" -name '*.webm' | sort))
if [[ "${#VIDEOS[@]}" -ne "${#CAPTIONS[@]}" ]]; then
    echo "Expected ${#CAPTIONS[@]} recorded videos under test-results/ (one per numbered test), found ${#VIDEOS[@]}" >&2
    exit 1
fi

echo "Building title cards and normalizing clips for concatenation..."
CLIPS=()
for i in "${!CAPTIONS[@]}"; do
    caption="${CAPTIONS[$i]}"
    title_clip="$WORK_DIR/title_$i.mp4"
    ffmpeg -y -f lavfi -i "color=c=0x2c3e50:s=${CLIP_WIDTH}x${CLIP_HEIGHT}:d=${TITLE_DUR}:r=${CLIP_FPS}" \
        -vf "drawtext=fontfile=${FONT}:text='${caption}':fontcolor=white:fontsize=44:x=(w-text_w)/2:y=(h-text_h)/2" \
        -c:v libx264 -preset ultrafast -pix_fmt yuv420p "$title_clip" >"$WORK_DIR/title_$i.log" 2>&1
    CLIPS+=("$title_clip")

    # Playwright's .webm recordings are VP8/VP9 at a variable frame rate;
    # re-encode each to the same codec/resolution/fixed-fps as the title
    # cards so the concat demuxer below can just stream-copy them together.
    video_clip="$WORK_DIR/clip_$i.mp4"
    ffmpeg -y -i "${VIDEOS[$i]}" \
        -vf "scale=${CLIP_WIDTH}:${CLIP_HEIGHT}" -r "$CLIP_FPS" \
        -c:v libx264 -preset ultrafast -pix_fmt yuv420p "$video_clip" >"$WORK_DIR/clip_$i.log" 2>&1
    CLIPS+=("$video_clip")
done

CONCAT_LIST="$WORK_DIR/concat.txt"
: >"$CONCAT_LIST"
for c in "${CLIPS[@]}"; do
    printf "file '%s'\n" "$c" >>"$CONCAT_LIST"
done
CONCAT_MP4="$WORK_DIR/concat.mp4"
ffmpeg -y -f concat -safe 0 -i "$CONCAT_LIST" -c copy "$CONCAT_MP4" >"$WORK_DIR/concat.log" 2>&1

echo "Converting to GIF..."
PALETTE="$WORK_DIR/palette.png"
ffmpeg -y -i "$CONCAT_MP4" \
    -vf "fps=${GIF_FPS},scale=${GIF_WIDTH}:-1:flags=lanczos,palettegen=max_colors=${GIF_MAX_COLORS}:stats_mode=diff" \
    "$PALETTE" >"$WORK_DIR/palette.log" 2>&1
ffmpeg -y -i "$CONCAT_MP4" -i "$PALETTE" \
    -lavfi "fps=${GIF_FPS},scale=${GIF_WIDTH}:-1:flags=lanczos [x]; [x][1:v] paletteuse=dither=bayer:bayer_scale=3" \
    "$OUTPUT_DIR/doxie-scanner-demo.gif" >"$WORK_DIR/gif.log" 2>&1

echo "Done: $OUTPUT_DIR/doxie-scanner-demo.gif"
