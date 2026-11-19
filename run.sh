#!/bin/bash
set -e

# Runs the locally-built image (see build.sh) with the required data
# volume and USB device passthrough wired in. Rebuild first if you've
# changed anything: ./build.sh && ./run.sh

IMAGE="${IMAGE:-doxie-scanner:local}"
DATA_DIR="${DATA_DIR:-./data}"
PORT="${PORT:-8080}"

mkdir -p "$DATA_DIR"

docker rm -f doxie-scanner >/dev/null 2>&1 || true

docker run -d \
	--name doxie-scanner \
	-p "${PORT}:8080" \
	-v "$(realpath "$DATA_DIR")":/data \
	-v /dev/bus/usb:/dev/bus/usb \
	-v /etc/localtime:/etc/localtime:ro \
	${DOXIE_PAPERLESS_URL:+-e DOXIE_PAPERLESS_URL} \
	${DOXIE_PAPERLESS_TOKEN:+-e DOXIE_PAPERLESS_TOKEN} \
	${DOXIE_BUTTON_DUPLEX:+-e DOXIE_BUTTON_DUPLEX} \
	${DOXIE_DISABLE_BUTTON:+-e DOXIE_DISABLE_BUTTON} \
	--device-cgroup-rule='c 189:* rmw' \
	"$IMAGE"

echo "doxie-scanner running at http://localhost:${PORT}"
echo "logs: docker logs -f doxie-scanner"
