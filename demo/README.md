# Demo recording

A scripted Playwright walkthrough of the UI that doubles as an
integration test and a demo recording, so re-recording after a UI change
is a rerun instead of a manual capture.

```
./record-demo.sh
```

## What it does

It tells one short story, in five clips, rather than showing every feature:
**scan** with auto-crop and auto-rotate (progress included), **pages come
back trimmed and upright** (and the page viewer's switcher), **drag pages
into order**, **export** a PDF or separate images, and **send to Paperless-ngx**.

1. Generates synthetic scans (`generate-seed-data.py`): an invoice, a letter,
   and "demo-receipts", a tilted receipt and business card on the scanner
   glass plus a letter fed in sideways. All content is made up.
2. Builds the `doxie-scanner` image and starts it with that data, plus
   `fake-paperless.py`, a tiny stand-in Paperless-ngx server, so Send to
   Paperless runs for real end to end. No USB device is attached.
3. Runs the app's **own** auto-crop and auto-rotate over the raw "demo-receipts"
   pages through its API, exactly what a real scan ends with, so the trimming,
   straightening and turning in the recording really happened.
4. Takes the README's still screenshots (`screenshots.spec.js`, desktop and
   phone width) into `../docs/screenshots/`.
5. Runs `demo.spec.js` inside the official `mcr.microsoft.com/playwright`
   Docker image (the host needs neither Node nor browsers) with real
   `expect()` assertions at every step, so it doubles as an integration test.
   Every run records video, and a visible cursor dot shows where clicks and
   drags land.
6. Converts the clips (with a title card in front of each) to a
   palette-optimized GIF at `output/doxie-scanner-demo.gif`.

Only the scanner's side is faked: the first clip intercepts the status and
progress requests at the network layer (`page.route`), because there is no
scanner to scan with. The pages it then delivers, the reordering, the exports
and the Paperless upload are all the real app.

`output/doxie-scanner-demo.gif` and the files in `../docs/screenshots/` are
committed on purpose: the top-level README embeds them, so they must exist in
the repo for GitHub to render them. Re-run the script and commit the new files
to update them.

This isn't a substitute for the Go test suite: it's the one layer that
exercises the real compiled binary, the real embedded frontend, and a
real browser together, which unit tests — mocked at the `driver.Driver`
seam — never do.

## Requires

`docker`, `python3` with Pillow (`pip install pillow`), and `ffmpeg`. Docker needs
to be able to pull the Playwright image the first time (about 3.5 GB).
Nothing else — Node, Playwright, and Chromium all run inside the
`mcr.microsoft.com/playwright` container, pinned to the same version as
`package.json`'s `@playwright/test` dependency.

## If the UI changes

`demo.spec.js` selects elements by ID/class, not screen coordinates, so
most markup changes just work. If you rename or restructure an element it
depends on (see the selectors in `demo.spec.js`), update the matching
locator there.
