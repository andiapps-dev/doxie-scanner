#!/bin/bash
set -e

# Usage: ./build.sh [docker|local]   (default: docker)
#   docker - build the Docker image (tag: doxie-scanner:local)
#   local  - build a native binary at ./doxie-scanner (needs cgo + libusb-1.0 dev headers)
MODE="${1:-docker}"

build_docker()
{
	docker build -t doxie-scanner:local .
}

build_local()
{
	CGO_ENABLED=1 go build -o doxie-scanner .
}

case "$MODE" in
	docker)
		build_docker
		;;
	local)
		build_local
		;;
	*)
		echo "Usage: $0 [docker|local]  (default: docker)" >&2
		exit 1
		;;
esac
