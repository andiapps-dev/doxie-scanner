#!/bin/bash
set -e

# Runs the full local check: vet, build, test with coverage. Needs cgo +
# libusb-1.0 dev headers (same as build.sh's "local" mode).
#
# Coverage is computed the same way CI enforces it: internal/scsiusb/usbdevice.go
# is the one file that only talks to real USB hardware and can't be
# meaningfully unit-tested, so it's excluded before computing the total
# (see README.md's Coverage section). Target: >=95%.

MIN_COVERAGE="${MIN_COVERAGE:-95}"

go vet ./...
go build ./...

go test -coverprofile=coverage.out ./...
grep -v internal/scsiusb/usbdevice.go coverage.out > coverage.filtered.out
go tool cover -html=coverage.filtered.out -o coverage.html

TOTAL=$(go tool cover -func=coverage.filtered.out | tail -1 | awk '{print $3}' | tr -d '%')

echo
echo "Total coverage (excluding usbdevice.go): ${TOTAL}%"
echo "Report: coverage.html"

awk -v total="$TOTAL" -v min="$MIN_COVERAGE" 'BEGIN { exit (total + 0 >= min + 0) ? 0 : 1 }' || {
	echo "Coverage ${TOTAL}% is below the ${MIN_COVERAGE}% target" >&2
	exit 1
}
