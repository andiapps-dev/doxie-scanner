# syntax=docker/dockerfile:1

# linux/amd64 only, by design: this binary is cgo + libusb-1.0, and
# cross-building arm64 via QEMU emulation is slow and fragile for a
# single-scanner-model utility with no arm64 hardware target today.

FROM --platform=linux/amd64 golang:1.24-alpine AS builder

RUN apk add --no-cache build-base pkgconf libusb-dev

# Set by docker-publish.yml to the git tag being released (e.g. v1.2.0);
# left as "dev" for a local `./build.sh` with no VERSION given.
ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/doxie-scanner .

# NOT scratch: gousb/cgo dynamically links libusb-1.0 at runtime, so the
# final image needs libc plus the libusb shared library — the one
# deliberate deviation from a pure-Go, scratch-based image.
FROM --platform=linux/amd64 alpine:3.20

RUN apk add --no-cache libusb ca-certificates && \
    adduser -D -u 1000 doxie

COPY --from=builder /out/doxie-scanner /usr/local/bin/doxie-scanner

ENV DOXIE_DATA_DIR=/data
ENV DOXIE_LISTEN_ADDR=:8080

VOLUME /data
EXPOSE 8080

# Not run as non-root by default: USB device nodes under /dev/bus/usb
# are typically root:root with restrictive permissions unless the
# provided udev rule (udev/99-doxie-scanner.rules) is installed on the
# host, in which case this can be switched to `USER doxie`. Documented
# in README's Docker section.
ENTRYPOINT ["/usr/local/bin/doxie-scanner"]
