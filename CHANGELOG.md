# Changelog

All notable changes to this project are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project uses [Semantic Versioning](https://semver.org/) — tags look
like `v1.2.0`, and each one gets its own section below.

## [Unreleased]

## [1.0.7] - 2026-07-21

### Added

- The web UI now supports English and Spanish. Language is auto-detected
  from the browser on first visit and can be overridden with a dropdown
  in the navbar; the choice is remembered for future visits. Adding
  another language is just a new `locales/<code>.json` file — no code
  changes needed.

## [1.0.6] - 2026-07-21

### Added

- "Extract Text" on any page's viewer runs OCR and shows the recognized
  text in a copy-pasteable box. Automatically corrects for a crooked
  scan first (via `unpaper`'s deskew pass) before handing it to
  `tesseract` — real scans are rarely perfectly straight, and a few
  degrees of skew measurably hurts OCR accuracy. Defaults to English
  (`DOXIE_OCR_LANG` to change it, plus the matching
  `tesseract-ocr-data-<lang>` package in a custom build). Text isn't
  cached — like Export, it's regenerated on demand each time.

## [1.0.5] - 2026-07-17

### Added

- The combine-into-PDF bar now shows a thumbnail of every selected page,
  draggable to set the final PDF's page order, with a per-thumbnail
  remove button — no more guessing what order pages will end up in or
  hunting down a page's original thumbnail just to deselect it.
  Thumbnails are labeled by their position in the combined document (1,
  2, 3...), not their original page number, since two pages from
  different scans can otherwise both claim to be "page 1". Click a
  thumbnail for a plain, action-free preview of that page.
- Combine-into-PDF and "Export scan as PDF" now let you choose PNG
  (lossless) or JPEG (smaller) for how pages are embedded, defaulting to
  JPEG. Real scans are noisy enough (CIS sensor noise) that PNG's
  lossless compression barely helps — a typical page runs 8-14MB as PNG
  but only 1-2MB as JPEG at quality 90 with no visible difference on text
  content; PNG remains available for scans of photos/art where JPEG
  artifacts would actually show.

- The per-page export menu now has two JPEG options — "JPEG (high
  quality)" (quality 100) and "JPEG (smaller)" (quality 90, the same
  tier PDF export uses) — so getting a compact single image no longer
  requires wrapping it in a PDF just to get the smaller file size.

### Changed

- Standalone JPEG image export (the per-page export menu, not PDF) is
  now quality 100 by default, up from 90 — since PDF export is now the
  deliberately smaller/convenience choice (see above), this is the
  deliberately higher-quality one. The per-page PDF export option itself
  now embeds JPEG (was PNG) with no separate toggle, since the plain PNG
  download right next to it in the same menu already covers the lossless
  case.

- Duplex scanning is now on by default. The previously documented
  front-side color cast didn't reproduce under isolated testing against
  a freshly power-cycled scanner (see README's "Duplex is on by
  default"); it was most likely a symptom of a degraded scanner state
  from earlier back-to-back testing, not an inherent hardware limit.
- Renamed the scan grid's "tile" terminology to "thumbnail" throughout
  (CSS classes, help text), matching what the combine bar already called
  its own page previews — the UI used both words for the same kind of
  element.

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
