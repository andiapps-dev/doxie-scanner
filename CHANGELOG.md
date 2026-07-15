# Changelog

All notable changes to this project are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project uses [Semantic Versioning](https://semver.org/) — tags look
like `v1.2.0`, and each one gets its own section below.

## [Unreleased]

### Changed

- Duplex scanning is now on by default. The previously documented
  front-side color cast didn't reproduce under isolated testing against
  a freshly power-cycled scanner (see README's "Duplex is on by
  default"); it was most likely a symptom of a degraded scanner state
  from earlier back-to-back testing, not an inherent hardware limit.

## [1.0.0] - 2026-07-15

### Added

- Initial release: a standalone web app for the Doxie Pro DX400 —
  scan (simplex/duplex/multi-page), review, rotate/crop, and export
  pages as PNG/JPEG/PDF, or combine pages from any past scans into one
  PDF, all from the browser
- Duplex sides are independent pages (not a front/back pair); deleting a
  page renumbers the rest to close the gap
- Drag-and-drop page reordering with live preview
- Docker image (linux/amd64), `docker-compose.yml`, and a udev rule for
  non-root USB access
