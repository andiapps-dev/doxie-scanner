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
   mounted, deliberately with no USB device attached, so most of the
   recording shows the real "scanner not connected" state rather than
   faking a connection.
3. Runs `demo.spec.js` against it inside the official
   `mcr.microsoft.com/playwright` Docker image (the host has neither
   Node nor browsers installed, and doesn't need them for this) — it
   clicks through the scan list, the language switcher (English/Spanish,
   confirming both static markup and dynamically-rendered text pick up
   the change), page viewer, rotate/crop (a real crop, dragging
   Cropper.js's own resize handle), Extract Text (a real unpaper+tesseract
   OCR pass, including deskew), drag-to-reorder pages, the
   combine-into-PDF bar (multi-job selection, drag-to-reorder, remove,
   view-only preview), rename, and delete, with real `expect()`
   assertions at each step. Every run records video, not just failures.
   A visible cursor dot is injected so the recording shows where each
   click/drag actually lands.
4. One segment ("Start and run a scan") is network-mocked via
   `page.route` — connected status, a running scan's progress ticking
   up, and its completed result — since this environment has no real
   scanner to demonstrate that flow against. Nothing server-side or in
   the real driver is touched; it only fakes what this one test's page
   receives over the network, reusing a real seed page's actual image
   bytes for the result so it still looks like a real document.
5. Converts the recorded videos (with a title card stitched in front of
   each) to a palette-optimized GIF at `output/doxie-scanner-demo.gif`.

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
