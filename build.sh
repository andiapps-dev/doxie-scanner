#!/bin/bash
set -e

# Usage: VERSION=v1.2.0 ./build.sh [docker|local]   (default: docker)
#   docker - build the Docker image (tag: doxie-scanner:local)
#   local  - build a native binary at ./doxie-scanner (needs cgo + libusb-1.0 dev headers)
# VERSION defaults to "dev" if unset — set it to bake a real version into
# the binary (surfaced via GET /api/version), matching a release tag.
MODE="${1:-docker}"
VERSION="${VERSION:-dev}"

build_docker()
{
	docker build --build-arg VERSION="$VERSION" -t doxie-scanner:local .
}

build_local()
{
	CGO_ENABLED=1 go build -ldflags="-X main.version=${VERSION}" -o doxie-scanner .
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
