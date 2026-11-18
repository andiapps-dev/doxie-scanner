# Changelog

All notable changes to this project are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project uses [Semantic Versioning](https://semver.org/) — tags look
like `v1.2.0`, and each one gets its own section below.

## [Unreleased]

### Added

- **Dark and light themes, and colour schemes.** A menu in the top bar sets the
  mode (Auto follows the system's light or dark setting, or Light / Dark) and one
  of five colour schemes (slate, the original look, ocean, forest, violet and
  rose), and holds the language choice, which moved there to make room on a
  phone. The choice is kept in the browser and applied before the page paints, so
  there is no flash of the wrong theme.
- **Search and date filter for scans.** Type words to match a scan's name or date
  and narrow by today, the last 7 or 30 days, this year, or a range of dates.
  *Select all* in select mode ticks only the scans the filter shows.
- **Rotate the selected pages together.** Two buttons in the selection bar turn
  every selected page left or right at once, across scans.
- **The latest scan opens when you arrive** (on a computer; on a phone the list
  is the starting point).

### Changed

- **Deleting asks first instead of offering Undo.** Deleting a page, a scan or
  several scans now shows what is about to go, with Cancel as the default (Enter,
  Escape or a click outside all keep it) and a red Delete button. The
  Undo-for-a-few-seconds is gone. After deleting the open scan, the next one in
  the list is shown instead of an empty pane.
- **Select mode shows what is in each scan.** Rows carry small previews of the
  first pages, and clicking a scan's name opens it beside the list, so you can
  look inside before ticking it for deletion.
- **Renaming a scan with Enter now leaves the field** instead of looking as if it
  were still being edited.

## [1.1.0] - 2026-11-17

### Added

- **README demo and screenshots re-recorded.** The old recording predated most
  of what the app does now. The new demo is shorter and shows the main story
  only: scan with auto-crop and auto-rotate, pages back trimmed and upright,
  drag to reorder, export a PDF or separate images, send to Paperless-ngx. It
  runs the app's own auto-crop and auto-rotate over seeded raw scans and a
  stand-in Paperless server, so only the scanner itself is faked. Desktop and
  phone screenshots come from the same script (`demo/record-demo.sh`).
- **One place to act on a selection.** Ticking pages used to show Send to
  Paperless and Export twice, in the scan header and in the bar at the bottom,
  with nothing saying why. The bottom bar is the place for a selection (it can
  hold pages from several scans and sets their order), so while anything is
  ticked the scan header's whole-scan Send and Export step aside, and come back
  when the selection is cleared. Both export menus now open with a line saying
  exactly what they export, such as "Exports the 2 selected pages" or "Exports
  all 4 pages of this scan".
- **Readability fixes.** The menu text was a faint grey; it now uses the body
  colour. The reorder hint no longer shows both its mouse and touch wording when
  the browser does not report a pointer type.
- **Export pages as separate images.** Choosing JPEG or PNG for several selected
  pages always produced a PDF: that menu only set the image format *inside* the
  PDF. The selection bar's *Export* button now has a menu with two clear groups,
  *PDF* and *Separate images (ZIP)* (PNG, high-quality JPEG, smaller JPEG), and
  the scan header's export menu offers the same for the whole scan. One selected
  page downloads as a plain image. New `POST /api/export/images` streams the ZIP
  page by page, so memory stays flat, and checks every page before sending a byte.
- **Page labels follow reordering.** Dragging a page to the first spot left it
  labelled "Page 2". Thumbnails are now labelled by their place in the scan.
  Dropping a page in the empty space before the first page, or above the grid,
  now also moves it to the front.
- **Send to Paperless-ngx.** Tick pages (from one scan or several) in the
  selection bar and send them as one document with a title, date,
  correspondent, document type and tags chosen from Paperless's own
  lists. Each scan lists what was sent from it, follows the upload until
  Paperless has filed it, links to the document and marks the sent
  pages. Turned on by `DOXIE_PAPERLESS_URL` and `DOXIE_PAPERLESS_TOKEN`
  (see `.env.example`). The Paperless client is from the
  jasonlearst/doxie-scanner fork.
- **Auto-crop & straighten**, on by default, with a switch on the
  new-scan card and an *Auto-crop pages* button on a finished scan
  (`POST /api/scans/{id}/auto-crop`). The scanner always reads its full
  width, so a photo, card or receipt comes out as a small picture in gray
  backing; each such page is trimmed to its sheet and straightened if
  tilted 0.5° or more. It acts only when at least 20% of the scan is
  backing. Run on 45 real scans, the sheet finder (from the fork) cropped
  all 19 photos and cards correctly or safely, but was confidently wrong
  on one letter page (a made-up 3.4° tilt and a crop through its footer);
  every letter page it found anything on had at most 11% to remove,
  against 26% or more on photos. A page it changes keeps its as-scanned
  image, is marked "auto-cropped", and *Restore original*
  (`POST /api/scans/{id}/pages/{n}/restore`) puts it back. When only one
  edge of a sheet is found, that side is trimmed and the other's backing
  is left.
- **Auto-rotate**, on by default where tesseract's `osd` data is
  installed (it is in the image), with a switch on the new-scan card and
  an *Auto-rotate pages* button on a finished scan
  (`POST /api/scans/{id}/auto-rotate`). Pages are turned upright from the
  text on them, so a sheet that fed in sideways or upside down comes out
  readable. It runs after the scanner has been released, never between
  sheets. A page with too little text to tell is left alone, and a turned
  page is marked so it can be turned back by hand.
- **Bulk delete.** *Select* above the scans list turns each scan into a
  checkbox row with *Select all* and a count; *Delete selected* asks
  first, saying how many scans and pages are going and, with Paperless
  set up, how many pages were never sent to it. Running scans can't be
  ticked. `POST /api/scans/bulk-delete` does the same through the API and
  reports which scans it deleted and which it skipped, and why.
  `GET /api/scans` now gives each scan's `unsentPages`.
- **Live scan progress.** A running scan says what it is doing instead of
  only counting pages: checking for paper, scanning sheet N, saving the
  pages, finishing up with the scanner, trimming pages to the sheet,
  turning pages upright (page N of M, with a progress bar) and loading the
  pages. It stays up until the scan's thumbnails have loaded, so the wait
  after the scanner stops is no longer unexplained. `GET /api/scans/{id}`
  reports `stage`, `stageDone` and `stageTotal` while a scan runs.
- **Page switcher.** The page viewer has previous/next arrows (and the
  left/right arrow keys) to step through the open scan's pages, with a
  "Page 2 of 4" counter.
- Scanner problems are reported in plain words: no paper in the feeder, a
  paper jam, the cover open, two sheets fed at once, or a sheet stopped
  partway through.

- **Thumbnails from the server.** The page grid and the selection bar show
  small cached JPEGs (`GET /api/scans/{id}/pages/{n}/thumb`) instead of
  loading each full page, which is 5 to 14 MB. Their cache can't go stale:
  it is keyed by the page file's size and modified time.
- **Undo for deletes.** Deleting a page or a scan hides it at once and offers
  Undo for 8 seconds before it is really deleted, replacing the
  confirmation box. Closing the page in that time leaves it undeleted.
- A running scan appears in the scan list, with a spinner, a few seconds in.
- When the scanner can't be reached, the new-scan card says what to try, by
  reason. With no scans yet, the empty pane says to load paper and press Start.
- Messages (sent, trimmed, restored, deleted) are toasts, not pop-up boxes,
  and counts read "1 page" and "3 pages" rather than "page(s)".

### Changed

- **The interface was reworked for clarity and for phones**, from a review of
  every screen at desktop and phone width. On a phone the scan list and the
  open scan take turns, with a back button, instead of stacking the scan far
  below the list; the scan header no longer runs off the screen. A scan's
  header holds its name (edited in place, with Save only while changed), one
  primary action, *Export PDF* and a *⋯* menu for auto-crop, auto-rotate and
  delete, in place of a row of eight buttons. Outline buttons are readable
  rather than pale enough to look disabled. The page viewer groups editing on
  the left and sharing on the right and shows what was done to the page.
  Pages have a visible tick-box with *Select all*. "Select for combine" is
  now "Select page", since the same selection feeds Send. Paperless tags are
  toggle chips instead of a multi-select list box.
- **Reordering pages works with touch.** The page grid and the selection bar
  were reordered with the browser's own drag and drop, which does not work on
  phones and tablets at all. Both now use pointer events, so a mouse, a finger
  and a pen share one implementation: a mouse drags at once, a finger presses
  and holds first (so a quick swipe still scrolls), a copy of the page follows
  the pointer, the list scrolls by itself near its edge, and Escape cancels. The
  page viewer's *⋯* menu also has *Move earlier* and *Move later* for exact moves.
- **The whole app fits in one window.** The page no longer grows and scrolls:
  the header and the new-scan card stay put, the scan list scrolls in its own
  panel, and the open scan's pages scroll in theirs, with the scan's header and
  the Select all row kept in view. The selection bar takes its height from the
  bottom and the panels shrink to fit. A window under about 520 pixels tall
  falls back to a normal scrolling page, so nothing is unreachable.
- **The scanner driver is now the one for Doxie's own protocol**, adapted
  from the jasonlearst/doxie-scanner fork (credited in the README): the
  vendor's 12-byte command set, with the scanner JPEG-encoding each side
  on the device, and one scan job held open across a whole stack so
  sheets feed back to back. Its commands and payloads come from USB
  captures of Doxie's own drivers. It is still registered as
  `doxie-dx400`, so `DOXIE_DRIVER` is unchanged, and scans stored by
  earlier versions open as before. In this project's own scans, the old
  driver sometimes gave the first scan of a session a magenta cast (4 of
  the 8 sessions measured, and never a later scan in a session); none of
  the 5 scans made with the new driver so far has.
- The web app starts scans with auto-crop and auto-rotate on. They are
  options on `POST /api/scans` (`autoCrop`, `autoRotate`) that stay off
  unless the request asks, so existing scripts keep getting pages exactly
  as scanned.
- A scan that is still running can't be edited (rename, reorder, delete a
  page, rotate, crop, restore): the request gets a 409. The scan is
  written back when it finishes, after the post-scan steps, so an edit
  made meanwhile would have been lost.
- `docker-compose.yml` and `run.sh` mount `/etc/localtime`, so scans are
  named in the host's local time rather than UTC.

- Dependencies: `golang.org/x/image` 0.46.0 to 0.48.0, and the release and
  CI workflows use `docker/setup-buildx-action` v4 and
  `docker/login-action` v4 (the updates Dependabot proposed, none of them a
  security advisory).

### Removed

- The original driver, which used the generic SCSI-2 scanner command set
  and read raw pixels, and the 10-byte command transport that went with
  it. They are in the git history.

### Fixed

- Deleting a scan that was still running removed its folder out from
  under the scanner. `DELETE /api/scans/{id}` now refuses it with a 409.
- Pages of a deleted scan stayed in the selection bar, where sending or
  combining them would fail. They now leave it.
- Deleting a page renumbers the pages after it, but the "sent to
  Paperless" records and the selection bar kept the old numbers, so a
  later send or combine could pick the wrong pages. Both now follow the
  renumbering (including after pages were reordered), and a page's kept
  original goes with it.

## [1.0.8] - 2026-09-14

### Changed

- Docker build now uses Go 1.27 and Alpine 3.24 (previously 1.24/3.20),
  and `golang.org/x/image` is updated from 0.12.0 to 0.46.0 — bumping
  `go.mod`'s minimum Go version to 1.26. CI now actually builds the
  Dockerfile on every push/PR (it previously only ran `go vet`/`build`/
  `test`, so a base-image bump could pass CI without the image itself
  ever being built).

### Fixed

- `TestNewJobID_Uniqueness` could rarely fail on entirely correct code:
  it drew 50 real random samples from a 65536-value space and asserted
  no collision, a ~2% chance event by the birthday paradox. Now
  deterministic.

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
