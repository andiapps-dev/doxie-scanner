# Demo recording

A scripted Playwright walkthrough of the UI that doubles as an
integration test and a demo recording, so re-recording after a UI change
is a rerun instead of a manual capture.

```
./record-demo.sh
```

## What it does

1. Generates two synthetic scan jobs (`generate-seed-data.py`) — an
   invoice and a cover letter, entirely made-up content, no real scanned
   documents involved.
2. Builds the `doxie-scanner` image and starts it with that seed data
   mounted, deliberately with no USB device attached, so the demo shows
   the real "scanner not connected" state rather than faking a
   connection.
3. Runs `demo.spec.js` against it inside the official
   `mcr.microsoft.com/playwright` Docker image (the host has neither
   Node nor browsers installed, and doesn't need them for this) — it
   clicks through the scan list, page viewer, rotate/crop, drag-to-reorder
   pages, the combine-into-PDF bar (multi-job selection, drag-to-reorder,
   remove, view-only preview), rename, and delete, with real `expect()`
   assertions at each step. Every run records video, not just failures.
4. Converts that video to a palette-optimized GIF at
   `output/doxie-scanner-demo.gif`.

`output/doxie-scanner-demo.gif` is committed on purpose — it's embedded
in the top-level README, so it needs to actually exist in the repo (not
be gitignored) for GitHub to render it. Re-run the script and commit the
new file to update it.

This isn't a substitute for the Go test suite: it's the one layer that
exercises the real compiled binary, the real embedded frontend, and a
real browser together, which unit tests — mocked at the `driver.Driver`
seam — never do.

## Requires

`docker`, `python3` with Pillow (`pip install pillow`), and `ffmpeg`.
Nothing else — Node, Playwright, and Chromium all run inside the
`mcr.microsoft.com/playwright` container, pinned to the same version as
`package.json`'s `@playwright/test` dependency.

## If the UI changes

`demo.spec.js` selects elements by ID/class, not screen coordinates, so
most markup changes just work. If you rename or restructure an element it
depends on (see the selectors in `demo.spec.js`), update the matching
locator there.
