(() => {
  'use strict';

  const state = {
    jobs: [],
    selectedJobId: null,
    selectedJob: null,
    // combineSelection maps "jobId|page" -> {jobId, page}; combineOrder is
    // the same keys, but as an ordered array — the actual order pages will
    // be combined in, which the user controls by dragging thumbnails in
    // the combine bar. The Map is for O(1) "is this selected" lookups
    // elsewhere (checkboxes); combineOrder is the source of truth for
    // display order and for the final combine request.
    bulkMode: false,
    bulkSelected: new Set(),
    combineSelection: new Map(),
    combineOrder: [],
    combineDragKey: null,
    scanPollTimer: null,
    cropper: null,
    // Bumped on every reload so image URLs change and the browser can't
    // serve a stale cached copy of a page that was just rotated/cropped.
    renderNonce: 0,
    // {jobId, page} for whichever page is open in the modal.
    viewer: null,
    dragSrcIndex: null,
    busyDepth: 0,
    scannerConnected: false,
    scanning: false,
  };

  const el = (id) => document.getElementById(id);
  const pageModal = () => bootstrap.Modal.getOrCreateInstance(el('page-modal'));

  // Wraps an async action with a full-screen busy spinner, so long-running
  // operations (rotate/crop/export/combine all round-trip through real
  // image processing server-side) give visible feedback instead of looking
  // like nothing happened. Nests safely via a depth counter in case one
  // busy action is ever triggered from within another.
  async function withBusy(fn) {
    state.busyDepth++;
    el('busy-overlay').classList.remove('d-none');
    try {
      return await fn();
    } finally {
      state.busyDepth--;
      if (state.busyDepth <= 0) {
        state.busyDepth = 0;
        el('busy-overlay').classList.add('d-none');
      }
    }
  }

  // translateErrorCode maps the backend's stable error `code` to a
  // translated, user-facing message; the raw `message` (often untranslatable
  // SCSI/USB/subprocess prose) is only ever logged to the console, never
  // shown in the UI.
  function translateErrorCode(code) {
    if (!code) return I18N.t('errors.generic');
    const key = `errors.${code}`;
    const value = I18N.t(key);
    return value === key ? I18N.t('errors.generic') : value;
  }

  async function api(path, opts) {
    const res = await fetch(path, opts);
    if (!res.ok && res.headers.get('content-type')?.includes('application/json')) {
      const body = await res.json().catch(() => null);
      const code = body?.error?.code;
      if (body?.error?.message) console.warn(`API error (${code || 'unknown'}): ${body.error.message}`);
      throw new Error(translateErrorCode(code));
    }
    if (!res.ok) {
      console.warn(`API error: request failed (${res.status})`);
      throw new Error(I18N.t('errors.generic'));
    }
    return res;
  }

  function combineKey(jobId, page) {
    return `${jobId}|${page}`;
  }

  function pageImageUrl(jobId, page) {
    const params = new URLSearchParams({ v: state.renderNonce });
    return `/api/scans/${jobId}/pages/${page}?${params}`;
  }

  // ---- Scanner connection status ----

  // updateStartButtonState keeps "Start scan" disabled whenever the
  // scanner isn't reachable, in addition to the existing disable while a
  // scan is already running — either condition alone should block it.
  function updateStartButtonState() {
    const btn = el('start-scan-btn');
    btn.disabled = !state.scannerConnected || state.scanning;
    if (state.scanning) {
      btn.title = I18N.t('scan.inProgressTitle');
    } else if (!state.scannerConnected) {
      btn.title = I18N.t('scan.waitingTitle');
    } else {
      btn.title = '';
    }
  }

  // renderFeatures shows the controls for optional server features: the
  // auto-rotate switch and button, and "Send to Paperless".
  function renderFeatures(data) {
    state.autoRotateAvailable = !!data.autoRotate;
    state.paperlessUrl = data.paperlessUrl || '';
    el('autorotate-wrap').classList.toggle('d-none', !state.autoRotateAvailable);
    el('job-autorotate-btn').classList.toggle('d-none', !state.autoRotateAvailable);
    el('combine-send-btn').classList.toggle('d-none', !state.paperlessUrl);
    if (!state.autoRotateRestored && state.autoRotateAvailable) {
      state.autoRotateRestored = true;
      try {
        const saved = localStorage.getItem('doxie-autorotate');
        if (saved !== null) el('autorotate-toggle').checked = saved === '1';
      } catch (e) { /* per-viewer convenience only */ }
    }
  }

  // Auto-crop is on by default; remember a switch-off for next time.
  try {
    if (localStorage.getItem('doxie-autocrop') === '0') el('autocrop-toggle').checked = false;
  } catch (e) { /* per-viewer convenience only */ }

  async function pollScannerStatus() {
    const badge = el('scanner-badge');
    try {
      const res = await api('/api/scanner/status');
      const data = await res.json();
      state.scannerConnected = data.connected;
      renderFeatures(data);
      if (data.connected) {
        badge.textContent = I18N.t('status.connected');
        badge.className = 'badge rounded-pill text-bg-success';
        badge.title = '';
      } else {
        badge.textContent = I18N.t('status.notConnected');
        badge.className = 'badge rounded-pill text-bg-danger';
        if (data.error?.message) console.warn(`Scanner status error (${data.error.code || 'unknown'}): ${data.error.message}`);
        badge.title = data.error ? translateErrorCode(data.error.code) : '';
      }
    } catch (e) {
      state.scannerConnected = false;
      badge.textContent = I18N.t('status.unknown');
      badge.className = 'badge rounded-pill text-bg-secondary';
    }
    updateStartButtonState();
  }

  // ---- Scan jobs ----

  async function loadJobs() {
    const res = await api('/api/scans');
    state.jobs = await res.json();
    renderJobList();
  }

  function renderJobList() {
    const list = el('job-list');
    list.innerHTML = '';
    if (state.jobs.length === 0) {
      list.innerHTML = `<div class="list-group-item text-muted">${I18N.t('jobs.empty')}</div>`;
      return;
    }
    for (const job of state.jobs) {
      if (state.bulkMode) {
        list.appendChild(renderBulkRow(job));
        continue;
      }
      const isActive = job.id === state.selectedJobId;
      const item = document.createElement('button');
      item.type = 'button';
      item.className = 'list-group-item list-group-item-action d-flex justify-content-between align-items-start';
      if (isActive) {
        item.classList.add('active');
        item.setAttribute('aria-current', 'true');
      }
      item.innerHTML = `
        <span>
          <div>${escapeHtml(job.name)}</div>
          <small class="job-meta ${isActive ? '' : 'text-muted'}">${escapeHtml(jobMetaLine(job))}</small>
        </span>
      `;
      item.addEventListener('click', () => selectJob(job.id));
      list.appendChild(item);
    }
  }

  // ---- Bulk delete ----

  // A scan that is still running can't be deleted, so it can't be ticked.
  function bulkSelectable(job) {
    return job.status !== 'running';
  }

  function jobMetaLine(job) {
    const when = new Date(job.createdAt).toLocaleString();
    return `${when} · ${job.pageCount} ${I18N.t('jobs.pageCountUnit')} · ${I18N.t(`status.${job.status}`)}`;
  }

  // In select mode each scan is a checkbox row; clicking it ticks it
  // instead of opening the scan.
  function renderBulkRow(job) {
    const row = document.createElement('label');
    row.className = 'list-group-item d-flex gap-2 align-items-start';
    const selectable = bulkSelectable(job);
    if (!selectable) row.title = I18N.t('bulk.runningTitle');
    row.innerHTML = `
      <input type="checkbox" class="form-check-input mt-1 bulk-check" ${state.bulkSelected.has(job.id) ? 'checked' : ''} ${selectable ? '' : 'disabled'}>
      <span>
        <div>${escapeHtml(job.name)}</div>
        <small class="job-meta text-muted">${escapeHtml(jobMetaLine(job))}</small>
      </span>`;
    row.querySelector('.bulk-check').addEventListener('change', (e) => {
      if (e.target.checked) state.bulkSelected.add(job.id);
      else state.bulkSelected.delete(job.id);
      updateBulkBar();
    });
    return row;
  }

  function updateBulkBar() {
    const selectable = state.jobs.filter(bulkSelectable);
    const n = state.bulkSelected.size;
    el('bulk-count').textContent = I18N.t('bulk.count', { count: n });
    el('bulk-delete-btn').disabled = n === 0;
    const all = el('bulk-all');
    all.checked = selectable.length > 0 && n === selectable.length;
    all.indeterminate = n > 0 && n < selectable.length;
  }

  function setBulkMode(on) {
    state.bulkMode = on;
    state.bulkSelected = new Set();
    el('bulk-bar').classList.toggle('d-none', !on);
    el('bulk-toggle-btn').textContent = I18N.t(on ? 'bulk.done' : 'bulk.select');
    renderJobList();
    if (on) updateBulkBar();
  }

  function toggleBulkAll() {
    if (el('bulk-all').checked) {
      state.bulkSelected = new Set(state.jobs.filter(bulkSelectable).map((j) => j.id));
    } else {
      state.bulkSelected = new Set();
    }
    renderJobList();
    updateBulkBar();
  }

  // forgetScans drops everything the page still holds about scans that no
  // longer exist: their pages in the selection bar (which would otherwise
  // be sent or combined and fail), and the open scan if it was one of them.
  function forgetScans(gone) {
    state.combineOrder = state.combineOrder.filter((k) => {
      const entry = state.combineSelection.get(k);
      if (entry && gone.has(entry.jobId)) {
        state.combineSelection.delete(k);
        return false;
      }
      return true;
    });
    renderCombineBar();
    if (gone.has(state.selectedJobId)) {
      state.selectedJobId = null;
      state.selectedJob = null;
      renderJobDetail();
    }
  }

  async function bulkDelete() {
    const chosen = state.jobs.filter((j) => state.bulkSelected.has(j.id));
    if (chosen.length === 0) return;
    const pages = chosen.reduce((sum, j) => sum + (j.pageCount || 0), 0);
    let message = I18N.t('confirm.bulkDelete', { count: chosen.length, pages });
    // Only meaningful when Paperless is set up: pages that never reached it
    // would be gone for good.
    const unsent = chosen.reduce((sum, j) => sum + (j.unsentPages || 0), 0);
    if (state.paperlessUrl && unsent > 0) {
      message += `\n\n${I18N.t('confirm.bulkUnsent', { count: unsent })}`;
    }
    if (!confirm(message)) return;

    await withBusy(async () => {
      const res = await api('/api/scans/bulk-delete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ids: chosen.map((j) => j.id) }),
      });
      const { deleted, skipped } = await res.json();
      forgetScans(new Set(deleted));
      state.bulkSelected = new Set(skipped.map((x) => x.id));
      await loadJobs();
      updateBulkBar();
      if (skipped.length > 0) {
        const running = skipped.filter((x) => x.reason === 'scan_running').length;
        alert(I18N.t('alert.bulkSkipped', { count: skipped.length, running }));
      }
    });
    if (state.bulkSelected.size === 0) setBulkMode(false);
  }

  async function selectJob(jobId) {
    state.selectedJobId = jobId;
    state.renderNonce++;
    const res = await api(`/api/scans/${jobId}`);
    state.selectedJob = await res.json();
    renderJobList();
    renderJobDetail();
    watchSent(jobId);
  }

  function renderJobDetail() {
    const job = state.selectedJob;
    if (!job) {
      el('job-detail-empty').classList.remove('d-none');
      el('job-detail').classList.add('d-none');
      return;
    }
    el('job-detail-empty').classList.add('d-none');
    el('job-detail').classList.remove('d-none');
    el('job-name-input').value = job.name;
    renderSentList(job);

    const grid = el('page-grid');
    grid.innerHTML = '';
    const pages = job.pages || [];
    if (pages.length === 0) {
      grid.innerHTML = `<p class="text-muted">${I18N.t('jobDetail.noPages')}</p>`;
      return;
    }
    for (const page of pages) {
      grid.appendChild(renderPageThumbnail(job.id, page));
    }
  }

  // renderPageThumbnail renders one thumbnail per page. Duplex scanning
  // produces two independent pages per physical sheet (see
  // storage.PageMeta) — there's nothing special about a "back" page here,
  // it's just the next page in the list, same as any other.
  function renderPageThumbnail(jobId, page) {
    const thumbnail = document.createElement('div');
    thumbnail.className = 'page-thumbnail';
    thumbnail.draggable = true;
    thumbnail.dataset.page = page.index;

    const key = combineKey(jobId, page.index);
    const selected = state.combineSelection.has(key);
    const src = pageImageUrl(jobId, page.index);
    const label = I18N.t('page.label', { n: page.index });
    const badges = [];
    if (page.autoCrop) badges.push(`✂ ${I18N.t('page.autoCroppedBadge')}`);
    if (page.autoRotated) badges.push(`↻ ${I18N.t('page.autoRotatedBadge')}`);
    if (sentPageSet(state.selectedJob).has(page.index)) badges.push(`✓ ${I18N.t('page.sentBadge')}`);

    thumbnail.innerHTML = `
      <img class="page-thumb" src="${src}" alt="${label}">
      <div class="page-thumbnail-overlay">
        <input type="checkbox" class="form-check-input combine-check" ${selected ? 'checked' : ''} title="${I18N.t('modal.selectForCombine')}">
      </div>
      <div class="page-thumbnail-footer">${label}${badges.length ? `<div class="page-thumbnail-badges">${badges.map(escapeHtml).join(' · ')}</div>` : ''}</div>
    `;

    thumbnail.querySelector('.page-thumb').addEventListener('click', () => openPageViewer(jobId, page.index));
    thumbnail.querySelector('.combine-check').addEventListener('change', () => {
      toggleCombine(jobId, page.index);
    });

    thumbnail.addEventListener('dragstart', (e) => {
      state.dragSrcIndex = page.index;
      thumbnail.classList.add('dragging');
      e.dataTransfer.effectAllowed = 'move';
    });
    thumbnail.addEventListener('dragend', () => {
      thumbnail.classList.remove('dragging');
      state.dragSrcIndex = null;
      // Re-render from the authoritative state: if a drop already
      // committed a reorder, selectJob() has (or will have) updated
      // state.selectedJob and this reflects that; if the drag was
      // cancelled with no drop, this simply restores the original order.
      renderJobDetail();
    });
    thumbnail.addEventListener('dragover', (e) => {
      e.preventDefault();
      if (state.dragSrcIndex == null || state.dragSrcIndex === page.index) return;
      // Move the actual dragged thumbnail in the DOM, live, to exactly
      // where it would land — what you see while dragging is what you
      // get, no separate indicator to interpret.
      const dragged = el('page-grid').querySelector('.page-thumbnail.dragging');
      if (!dragged) return;
      const rect = thumbnail.getBoundingClientRect();
      const before = e.clientX - rect.left < rect.width / 2;
      thumbnail.parentNode.insertBefore(dragged, before ? thumbnail : thumbnail.nextSibling);
    });
    thumbnail.addEventListener('drop', (e) => {
      e.preventDefault();
      if (state.dragSrcIndex == null) return;
      commitReorder();
    });

    return thumbnail;
  }

  // commitReorder reads whatever order the page thumbnails are currently
  // in (already rearranged live during dragover) and persists it — the
  // grid itself is the source of truth for the drop, not a recomputed
  // position.
  function commitReorder() {
    const jobId = state.selectedJobId;
    const order = Array.from(el('page-grid').querySelectorAll('.page-thumbnail')).map((t) => Number(t.dataset.page));
    withBusy(async () => {
      await api(`/api/scans/${jobId}/pages/order`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ order }),
      });
      await selectJob(jobId);
    });
  }

  function toggleCombine(jobId, page) {
    const key = combineKey(jobId, page);
    if (state.combineSelection.has(key)) {
      state.combineSelection.delete(key);
      state.combineOrder = state.combineOrder.filter((k) => k !== key);
    } else {
      state.combineSelection.set(key, { jobId, page });
      state.combineOrder.push(key);
    }
    renderJobDetail();
    renderCombineBar();
    if (state.viewer && state.viewer.jobId === jobId && state.viewer.page === page) {
      el('pm-combine-check').checked = state.combineSelection.has(key);
    }
  }

  // removeFromCombine deselects one page directly from the combine bar's
  // thumbnail strip (rather than from wherever its page thumbnail happens
  // to be, which might be a different scan entirely than the one
  // currently open).
  function removeFromCombine(key) {
    const entry = state.combineSelection.get(key);
    if (!entry) return;
    toggleCombine(entry.jobId, entry.page);
  }

  function renderCombineBar() {
    const bar = el('combine-bar');
    const count = state.combineOrder.length;
    el('combine-count-text').textContent = I18N.t('combine.selected', { count });
    bar.classList.toggle('d-none', count === 0);

    const thumbs = el('combine-thumbs');
    thumbs.innerHTML = '';
    state.combineOrder.forEach((key, i) => {
      const entry = state.combineSelection.get(key);
      if (entry) thumbs.appendChild(renderCombineThumb(key, entry, i + 1));
    });
  }

  // renderCombineThumb renders one draggable, removable thumbnail in the
  // combine bar. Dragging reorders combineOrder directly (the same
  // live-DOM-reorder technique as the main page grid); there's no server
  // round-trip involved, since combine order only matters at the moment
  // "Combine into PDF" is clicked. `position` is this thumbnail's place
  // in the combined document (1, 2, 3...), not its original page number
  // within its source scan — two pages from different scans can both be
  // "page 1" in their own right, so labeling by source page number was
  // ambiguous here.
  function renderCombineThumb(key, entry, position) {
    const thumbnail = document.createElement('div');
    thumbnail.className = 'combine-thumb';
    thumbnail.draggable = true;
    thumbnail.dataset.key = key;

    const src = pageImageUrl(entry.jobId, entry.page);
    thumbnail.innerHTML = `
      <img src="${src}" alt="${I18N.t('combine.thumbAlt', { n: position })}">
      <button type="button" class="combine-thumb-remove" title="${I18N.t('combine.removeTitle')}">&times;</button>
      <div class="combine-thumb-label">${position}</div>
    `;

    thumbnail.querySelector('img').addEventListener('click', () => {
      openPageViewer(entry.jobId, entry.page, { viewOnly: true });
    });

    thumbnail.querySelector('.combine-thumb-remove').addEventListener('click', () => {
      removeFromCombine(key);
    });

    thumbnail.addEventListener('dragstart', (e) => {
      state.combineDragKey = key;
      thumbnail.classList.add('dragging');
      e.dataTransfer.effectAllowed = 'move';
    });
    thumbnail.addEventListener('dragend', () => {
      thumbnail.classList.remove('dragging');
      state.combineDragKey = null;
      renderCombineBar();
    });
    thumbnail.addEventListener('dragover', (e) => {
      e.preventDefault();
      if (state.combineDragKey == null || state.combineDragKey === key) return;
      const dragged = el('combine-thumbs').querySelector('.combine-thumb.dragging');
      if (!dragged) return;
      const rect = thumbnail.getBoundingClientRect();
      const before = e.clientX - rect.left < rect.width / 2;
      thumbnail.parentNode.insertBefore(dragged, before ? thumbnail : thumbnail.nextSibling);
    });
    thumbnail.addEventListener('drop', (e) => {
      e.preventDefault();
      if (state.combineDragKey == null) return;
      // Commit the order directly from the live DOM arrangement — what
      // you saw while dragging is what gets combined.
      state.combineOrder = Array.from(el('combine-thumbs').querySelectorAll('.combine-thumb')).map((t) => t.dataset.key);
    });

    return thumbnail;
  }

  // ---- Page viewer / editor modal ----

  // opts.viewOnly hides every action (rotate/crop/export/delete/combine
  // checkbox) for a plain look-without-touching preview — used when
  // opening from the combine bar's thumbnails, where you're reviewing a
  // pick, not editing it.
  function openPageViewer(jobId, pageIndex, opts = {}) {
    state.viewer = { jobId, page: pageIndex, viewOnly: !!opts.viewOnly };
    loadViewerImage();
    el('page-modal-view-actions').classList.toggle('d-none', !!opts.viewOnly);
    pageModal().show();
  }

  function loadViewerImage() {
    const { jobId, page } = state.viewer;
    el('page-modal-image').src = pageImageUrl(jobId, page);
    el('pm-combine-check').checked = state.combineSelection.has(combineKey(jobId, page));
    const meta = ((state.selectedJob && state.selectedJob.id === jobId && state.selectedJob.pages) || []).find((p) => p.index === page);
    el('pm-restore').classList.toggle('d-none', !(meta && meta.original));
    updateViewerNav();
  }

  // The pages the viewer can step through: the open scan's, in the order
  // shown. Opened from the combine bar's thumbnails (viewOnly), the viewer
  // looks at one chosen page and has nothing to step through.
  function viewerPages() {
    const job = state.selectedJob;
    if (!state.viewer || state.viewer.viewOnly || !job || job.id !== state.viewer.jobId) return [];
    return job.pages || [];
  }

  // The switcher is off while cropping or reading OCR text, which belong
  // to the page on screen.
  function viewerBusyWithPage() {
    return !!state.cropper || !el('page-modal-ocr-wrap').classList.contains('d-none');
  }

  function updateViewerNav() {
    if (!state.viewer) return;
    const { page } = state.viewer;
    const pages = viewerPages();
    const pos = pages.findIndex((p) => p.index === page);
    const title = el('page-modal-title');
    title.textContent = pos >= 0
      ? I18N.t('modal.pageOf', { n: pos + 1, total: pages.length })
      : I18N.t('page.label', { n: page });
    const show = pages.length > 1 && pos >= 0;
    const busy = viewerBusyWithPage();
    for (const [id, target] of [['pm-prev', pos - 1], ['pm-next', pos + 1]]) {
      const btn = el(id);
      btn.classList.toggle('invisible', !show);
      btn.disabled = busy || target < 0 || target >= pages.length;
    }
  }

  function stepViewer(delta) {
    if (!state.viewer || viewerBusyWithPage()) return;
    const pages = viewerPages();
    const pos = pages.findIndex((p) => p.index === state.viewer.page);
    const next = pages[pos + delta];
    if (pos < 0 || !next) return;
    state.viewer.page = next.index;
    loadViewerImage();
  }

  async function rotateCurrentPage(degrees) {
    const { jobId, page } = state.viewer;
    await withBusy(async () => {
      await api(`/api/scans/${jobId}/pages/${page}/rotate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ degrees }),
      });
      await selectJob(jobId);
      loadViewerImage();
    });
  }

  function startCrop() {
    const img = el('page-modal-image');
    el('page-modal-view-actions').classList.add('d-none');
    el('page-modal-crop-actions').classList.remove('d-none');
    // Cropper.js reads the image's *current* rendered box size (still
    // bounded by the same max-height the plain viewer uses) and builds its
    // interactive canvas to match — so the modal never grows and the
    // action buttons stay in place. Do not relax any size constraint here.
    state.cropper = new Cropper(img, { viewMode: 1, autoCropArea: 1 });
    updateViewerNav();
  }

  function endCropUI() {
    if (state.cropper) {
      state.cropper.destroy();
      state.cropper = null;
    }
    el('page-modal-view-actions').classList.remove('d-none');
    el('page-modal-crop-actions').classList.add('d-none');
    updateViewerNav();
  }

  function cancelCrop() {
    endCropUI();
    loadViewerImage();
  }

  async function saveCrop() {
    if (!state.cropper || !state.viewer) return;
    const data = state.cropper.getData(true);
    const { jobId, page } = state.viewer;
    await withBusy(async () => {
      await api(`/api/scans/${jobId}/pages/${page}/crop`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ x: data.x, y: data.y, width: data.width, height: data.height }),
      });
      endCropUI();
      await selectJob(jobId);
      loadViewerImage();
    });
  }

  // ---- OCR ----

  // Swaps the image view for a plain-text result view (same show-one-
  // hide-the-other pattern startCrop/endCropUI already use for the crop
  // UI), runs the extraction, and fills the textarea with the result.
  async function startOcr() {
    if (!state.viewer) return;
    const { jobId, page } = state.viewer;
    el('page-modal-view-actions').classList.add('d-none');
    el('page-modal-image-wrap').classList.add('d-none');
    el('page-modal-ocr-wrap').classList.remove('d-none');
    el('page-modal-ocr-actions').classList.remove('d-none');
    el('pm-ocr-text').value = '';
    el('pm-ocr-hint').textContent = I18N.t('modal.ocrExtracting');
    updateViewerNav();

    await withBusy(async () => {
      try {
        const res = await api(`/api/scans/${jobId}/pages/${page}/ocr`);
        const { text } = await res.json();
        el('pm-ocr-text').value = text || '';
        el('pm-ocr-hint').textContent = text ? '' : I18N.t('modal.ocrNoText');
      } catch (e) {
        el('pm-ocr-hint').textContent = e.message;
      }
    });
  }

  function endOcrUI() {
    el('page-modal-view-actions').classList.remove('d-none');
    el('page-modal-image-wrap').classList.remove('d-none');
    el('page-modal-ocr-wrap').classList.add('d-none');
    el('page-modal-ocr-actions').classList.add('d-none');
    updateViewerNav();
  }

  // Tries the Clipboard API first, but that's only available in a
  // secure context (HTTPS or localhost) — this app is typically reached
  // over plain HTTP on a home LAN, where navigator.clipboard is simply
  // undefined. textarea.select() has no such restriction and always
  // works, so it's the fallback rather than an error message.
  async function copyOcrText() {
    const textarea = el('pm-ocr-text');
    const btn = el('pm-ocr-copy');
    const originalLabel = btn.textContent;

    if (navigator.clipboard) {
      try {
        await navigator.clipboard.writeText(textarea.value);
        btn.textContent = I18N.t('modal.ocrCopied');
      } catch (e) {
        textarea.select();
        btn.textContent = I18N.t('modal.ocrSelected');
      }
    } else {
      textarea.select();
      btn.textContent = I18N.t('modal.ocrSelected');
    }
    setTimeout(() => { btn.textContent = originalLabel; }, 1500);
  }

  // renumberSelection rewrites the selected pages of one scan after a
  // deletion: newIndex maps each surviving page's old number to its new
  // one, and a page that is gone drops out of the selection.
  function renumberSelection(jobId, newIndex) {
    const order = [];
    const selection = new Map();
    for (const key of state.combineOrder) {
      const entry = state.combineSelection.get(key);
      if (!entry) continue;
      if (entry.jobId !== jobId) {
        order.push(key);
        selection.set(key, entry);
      } else if (newIndex.has(entry.page)) {
        const moved = { jobId, page: newIndex.get(entry.page) };
        const movedKey = combineKey(jobId, moved.page);
        order.push(movedKey);
        selection.set(movedKey, moved);
      }
    }
    state.combineOrder = order;
    state.combineSelection = selection;
  }

  async function deleteCurrentPage() {
    if (!state.viewer) return;
    const { jobId, page } = state.viewer;
    if (!confirm(I18N.t('confirm.deletePage'))) return;
    await withBusy(async () => {
      // The server renumbers the remaining pages by their position, so the
      // selection (kept by page number) must follow, or "send" and
      // "combine" would pick the wrong pages afterwards.
      const remaining = ((state.selectedJob && state.selectedJob.id === jobId && state.selectedJob.pages) || [])
        .filter((p) => p.index !== page);
      const newIndex = new Map(remaining.map((p, i) => [p.index, i + 1]));
      await api(`/api/scans/${jobId}/pages/${page}`, { method: 'DELETE' });
      renumberSelection(jobId, newIndex);
      renderCombineBar();
      pageModal().hide();
      await selectJob(jobId);
      await loadJobs();
    });
  }

  // ---- Export / combine ----

  async function downloadCombinedPDF(pages, title, imageFormat) {
    await withBusy(async () => {
      const res = await api('/api/export/combine', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ pages, title, imageFormat }),
      });
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${title}.pdf`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    });
  }

  async function combineSelected() {
    const pages = state.combineOrder
      .map((key) => state.combineSelection.get(key))
      .filter(Boolean)
      .map((p) => ({ jobId: p.jobId, page: p.page }));
    const title = el('combine-title').value.trim() || 'combined';
    await downloadCombinedPDF(pages, title, el('combine-format').value);
  }

  async function exportWholeScan() {
    const job = state.selectedJob;
    if (!job || !job.pages || job.pages.length === 0) {
      alert(I18N.t('alert.noPages'));
      return;
    }
    const pages = job.pages.map((p) => ({ jobId: job.id, page: p.index }));
    await downloadCombinedPDF(pages, job.name, el('job-export-format').value);
  }

  // ---- Auto-rotate ----

  async function autoRotateScan() {
    const job = state.selectedJob;
    if (!job) return;
    await withBusy(async () => {
      const before = (job.pages || []).map((p) => p.autoRotated || 0);
      const res = await api(`/api/scans/${job.id}/auto-rotate`, { method: 'POST' });
      const updated = await res.json();
      const turned = (updated.pages || []).filter((p, i) => (p.autoRotated || 0) !== before[i]).length;
      await selectJob(job.id);
      alert(I18N.t(turned ? 'jobDetail.autoRotated' : 'jobDetail.noneRotated', { n: turned }));
    });
  }

  async function autoCropScan() {
    const job = state.selectedJob;
    if (!job) return;
    await withBusy(async () => {
      const res = await api(`/api/scans/${job.id}/auto-crop`, { method: 'POST' });
      const updated = await res.json();
      const cropped = (updated.pages || []).filter((p, i) => !!p.autoCrop !== !!(job.pages[i] && job.pages[i].autoCrop)).length;
      await selectJob(job.id);
      alert(I18N.t(cropped ? 'jobDetail.autoCropped' : 'jobDetail.noneCropped', { n: cropped }));
    });
  }

  // Puts the page on screen back exactly as scanned (only offered for a
  // page auto-crop changed, which is the one whose original is kept).
  async function restoreCurrentPage() {
    if (!state.viewer) return;
    const { jobId, page } = state.viewer;
    await withBusy(async () => {
      await api(`/api/scans/${jobId}/pages/${page}/restore`, { method: 'POST' });
      await selectJob(jobId);
      loadViewerImage();
    });
  }

  // ---- Sent to Paperless ----

  // sentPageSet is the page numbers of a scan already in a Paperless
  // document that has not failed.
  function sentPageSet(job) {
    const set = new Set();
    for (const d of (job && job.sent) || []) {
      if (d.status !== 'failed') (d.pages || []).forEach((n) => set.add(n));
    }
    return set;
  }

  function sentIsPending(job) {
    return ((job && job.sent) || []).some((d) => d.status === 'queued' || d.status === 'processing');
  }

  function renderSentList(job) {
    const box = el('job-sent');
    const sent = job.sent || [];
    box.classList.toggle('d-none', sent.length === 0);
    if (sent.length === 0) { box.innerHTML = ''; return; }
    const rows = sent.map((d) => {
      let status = I18N.t(`sent.${d.status}`);
      if (d.status === 'done' && d.documentId && state.paperlessUrl) {
        const href = `${state.paperlessUrl.replace(/\/+$/, '')}/documents/${d.documentId}/details`;
        status += ` · <a href="${escapeHtml(href)}" target="_blank" rel="noopener">${I18N.t('sent.open')}</a>`;
      } else if (d.status === 'failed' && d.error) {
        status += `: ${escapeHtml(d.error)}`;
      }
      const cls = d.status === 'failed' ? 'text-danger' : d.status === 'done' ? 'text-success' : 'text-muted';
      return `<li><strong>${escapeHtml(d.title)}</strong> <span class="text-muted">(${I18N.t('sent.pages', { pages: (d.pages || []).join(', ') })})</span> <span class="${cls}">${status}</span></li>`;
    });
    box.innerHTML = `<div class="small"><div class="fw-semibold">${I18N.t('sent.heading')}</div><ul class="mb-0 ps-3">${rows.join('')}</ul></div>`;
  }

  // While an upload is still with Paperless, ask how it is doing every few
  // seconds and show the answer; stops once nothing is pending or another
  // scan is opened.
  function watchSent(jobId) {
    if (state.sentTimer) clearTimeout(state.sentTimer);
    state.sentTimer = null;
    if (!state.paperlessUrl || !state.selectedJob || state.selectedJob.id !== jobId || !sentIsPending(state.selectedJob)) return;
    state.sentTimer = setTimeout(async () => {
      try {
        const res = await api(`/api/scans/${jobId}/sent/refresh`, { method: 'POST' });
        const job = await res.json();
        if (state.selectedJob && state.selectedJob.id === jobId) {
          state.selectedJob = job;
          renderJobDetail();
        }
      } catch (e) {
        console.warn(e.message);
      }
      watchSent(jobId);
    }, 3000);
  }

  function selectedPagesInOrder() {
    return state.combineOrder
      .map((key) => state.combineSelection.get(key))
      .filter(Boolean)
      .map((p) => ({ jobId: p.jobId, page: p.page }));
  }

  function fillSelect(sel, items, placeholder) {
    sel.innerHTML = '';
    if (placeholder) {
      const o = document.createElement('option');
      o.value = '0';
      o.textContent = placeholder;
      sel.appendChild(o);
    }
    for (const it of items) {
      const o = document.createElement('option');
      o.value = String(it.id);
      o.textContent = it.name;
      sel.appendChild(o);
    }
  }

  async function openSendDialog() {
    const pages = selectedPagesInOrder();
    if (pages.length === 0) return;
    el('send-error').classList.add('d-none');
    el('send-summary').textContent = I18N.t('send.summary', { count: pages.length });
    el('send-title').value = el('combine-title').value.trim();
    el('send-created').value = '';
    try {
      await withBusy(async () => {
        const res = await api('/api/paperless/metadata');
        const m = await res.json();
        const none = I18N.t('send.letPaperless');
        fillSelect(el('send-correspondent'), m.correspondents || [], none);
        fillSelect(el('send-doctype'), m.documentTypes || [], none);
        fillSelect(el('send-tags'), m.tags || [], '');
      });
    } catch (e) {
      alert(`${I18N.t('send.loadFailed')} ${e.message}`);
      return;
    }
    bootstrap.Modal.getOrCreateInstance(el('send-modal')).show();
  }

  async function submitSend() {
    const pages = selectedPagesInOrder();
    const tags = Array.from(el('send-tags').selectedOptions).map((o) => Number(o.value));
    const body = {
      pages,
      title: el('send-title').value.trim(),
      created: el('send-created').value,
      correspondent: Number(el('send-correspondent').value) || 0,
      documentType: Number(el('send-doctype').value) || 0,
      tags,
    };
    const btn = el('send-submit');
    btn.disabled = true;
    btn.textContent = I18N.t('send.sending');
    try {
      await api('/api/paperless/send', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
    } catch (e) {
      const err = el('send-error');
      err.textContent = e.message;
      err.classList.remove('d-none');
      return;
    } finally {
      btn.disabled = false;
      btn.textContent = I18N.t('send.submit');
    }
    bootstrap.Modal.getOrCreateInstance(el('send-modal')).hide();
    state.combineSelection.clear();
    state.combineOrder = [];
    renderCombineBar();
    await loadJobs();
    if (state.selectedJobId) await selectJob(state.selectedJobId);
  }

  // ---- Start scan + progress polling ----

  async function startScan() {
    const duplex = el('duplex-toggle').checked;
    const autoRotate = state.autoRotateAvailable && el('autorotate-toggle').checked;
    const autoCrop = el('autocrop-toggle').checked;
    try {
      localStorage.setItem('doxie-autorotate', autoRotate ? '1' : '0');
      localStorage.setItem('doxie-autocrop', autoCrop ? '1' : '0');
    } catch (e) { /* per-viewer convenience only */ }
    state.scanning = true;
    updateStartButtonState();
    try {
      const res = await api('/api/scans', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ duplex, autoRotate, autoCrop }),
      });
      const data = await res.json();
      watchScanProgress(data.jobId);
    } catch (e) {
      alert(e.message);
      state.scanning = false;
      updateStartButtonState();
    }
  }

  // What the scan is doing, in words: the step the server reports (so a
  // scan that has stopped feeding but is still working never looks
  // stuck), then how many pages exist so far.
  function scanProgressText(job) {
    const stage = job.stage
      ? I18N.t(`scan.stage.${job.stage}`, { n: job.stageDone || 0, total: job.stageTotal || 0 })
      : I18N.t('scan.stage.starting');
    const pages = job.pagesScanned ?? job.pageCount ?? 0;
    return pages > 0 ? `${stage} · ${I18N.t('scan.pagesSoFar', { count: pages })}` : stage;
  }

  // showScanProgress fills the progress block. fraction is 0..1 for a step
  // that knows how far along it is (turning pages upright), or null for
  // one that doesn't, which shows a moving bar instead.
  function showScanProgress(text, fraction) {
    el('scan-progress').classList.remove('d-none');
    el('scan-progress-text').textContent = text;
    const bar = el('scan-progress-bar');
    const known = fraction != null;
    bar.classList.toggle('progress-bar-striped', !known);
    bar.classList.toggle('progress-bar-animated', !known);
    bar.style.width = known ? `${Math.round(fraction * 100)}%` : '100%';
  }

  // Page thumbnails are the full stored images, so on a slow link they
  // can take a while after the scan itself is done; wait for them (but not
  // forever) so the progress stays up until the scan can really be seen.
  async function waitForThumbnails(maxMs = 30000) {
    const imgs = Array.from(document.querySelectorAll('#page-grid img.page-thumb'));
    const loaded = Promise.allSettled(imgs.map((img) => (img.decode ? img.decode() : Promise.resolve())));
    await Promise.race([loaded, new Promise((resolve) => setTimeout(resolve, maxMs))]);
  }

  function watchScanProgress(jobId) {
    showScanProgress(I18N.t('scan.stage.starting'), null);
    if (state.scanPollTimer) clearInterval(state.scanPollTimer);
    state.scanPollTimer = setInterval(async () => {
      const res = await api(`/api/scans/${jobId}`);
      const job = await res.json();
      if (job.status === 'running') {
        const counted = (job.stage === 'rotating' || job.stage === 'cropping') && job.stageTotal > 0;
        showScanProgress(scanProgressText(job), counted ? (job.stageDone - 1) / job.stageTotal : null);
        return;
      }
      clearInterval(state.scanPollTimer);
      state.scanPollTimer = null;
      state.scanning = false;
      updateStartButtonState();
      showScanProgress(I18N.t('scan.stage.loading'), null);
      try {
        await loadJobs();
        await selectJob(jobId);
        await waitForThumbnails();
      } finally {
        el('scan-progress').classList.add('d-none');
      }
    }, 500);
  }

  // ---- Rename / delete job ----

  async function renameJob() {
    const name = el('job-name-input').value.trim();
    if (!name || !state.selectedJobId) return;
    await api(`/api/scans/${state.selectedJobId}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    });
    await loadJobs();
  }

  async function deleteJob() {
    if (!state.selectedJobId) return;
    if (!confirm(I18N.t('confirm.deleteScan'))) return;
    await withBusy(async () => {
      const id = state.selectedJobId;
      await api(`/api/scans/${id}`, { method: 'DELETE' });
      forgetScans(new Set([id]));
      await loadJobs();
    });
  }

  function escapeHtml(s) {
    const div = document.createElement('div');
    div.textContent = s;
    return div.innerHTML;
  }

  // ---- Wire up ----

  el('start-scan-btn').addEventListener('click', startScan);
  el('bulk-toggle-btn').addEventListener('click', () => setBulkMode(!state.bulkMode));
  el('bulk-all').addEventListener('change', toggleBulkAll);
  el('bulk-delete-btn').addEventListener('click', bulkDelete);
  el('job-autorotate-btn').addEventListener('click', autoRotateScan);
  el('job-autocrop-btn').addEventListener('click', autoCropScan);
  el('pm-restore').addEventListener('click', restoreCurrentPage);
  el('combine-send-btn').addEventListener('click', openSendDialog);
  el('send-submit').addEventListener('click', submitSend);
  el('pm-prev').setAttribute('aria-label', I18N.t('modal.prevPage'));
  el('pm-next').setAttribute('aria-label', I18N.t('modal.nextPage'));
  el('pm-prev').addEventListener('click', () => stepViewer(-1));
  el('pm-next').addEventListener('click', () => stepViewer(1));
  // Arrow keys step through the pages while the viewer is open, unless
  // the person is typing (the crop box and OCR text area keep their keys).
  document.addEventListener('keydown', (e) => {
    if (!el('page-modal').classList.contains('show')) return;
    if (e.target.closest && e.target.closest('input, textarea, select')) return;
    if (e.key === 'ArrowLeft') { stepViewer(-1); e.preventDefault(); }
    if (e.key === 'ArrowRight') { stepViewer(1); e.preventDefault(); }
  });
  el('job-rename-btn').addEventListener('click', renameJob);
  el('job-delete-btn').addEventListener('click', deleteJob);
  el('job-export-pdf-btn').addEventListener('click', exportWholeScan);
  el('combine-btn').addEventListener('click', combineSelected);
  el('combine-clear-btn').addEventListener('click', () => {
    state.combineSelection.clear();
    state.combineOrder = [];
    renderJobDetail();
    renderCombineBar();
  });

  el('pm-rotate-left').addEventListener('click', () => rotateCurrentPage(270));
  el('pm-rotate-right').addEventListener('click', () => rotateCurrentPage(90));
  el('pm-crop-start').addEventListener('click', startCrop);
  el('pm-crop-cancel').addEventListener('click', cancelCrop);
  el('pm-crop-save').addEventListener('click', saveCrop);
  el('pm-extract-text').addEventListener('click', startOcr);
  el('pm-ocr-back').addEventListener('click', endOcrUI);
  el('pm-ocr-copy').addEventListener('click', copyOcrText);
  el('pm-delete').addEventListener('click', deleteCurrentPage);
  el('pm-combine-check').addEventListener('change', (e) => {
    if (!state.viewer) return;
    const { jobId, page } = state.viewer;
    toggleCombine(jobId, page);
    e.target.checked = state.combineSelection.has(combineKey(jobId, page));
  });
  document.querySelectorAll('.pm-export-link').forEach((a) => {
    a.addEventListener('click', (e) => {
      e.preventDefault();
      if (!state.viewer) return;
      const { jobId, page } = state.viewer;
      const params = { format: a.dataset.format };
      if (a.dataset.quality) params.quality = a.dataset.quality;
      const qs = new URLSearchParams(params);
      window.open(`/api/scans/${jobId}/pages/${page}/export?${qs}`, '_blank');
    });
  });
  el('page-modal').addEventListener('hidden.bs.modal', () => {
    endCropUI();
    endOcrUI();
    state.viewer = null;
  });

  // Fallback for dropping into empty grid space past the last thumbnail
  // (e.g. an incomplete last row) — wired once here rather than in
  // renderPageThumbnail, since #page-grid itself is never recreated, only
  // its children (renderJobDetail rebuilds those on every render).
  const pageGrid = el('page-grid');
  pageGrid.addEventListener('dragover', (e) => {
    if (e.target !== pageGrid || state.dragSrcIndex == null) return;
    e.preventDefault();
    const dragged = pageGrid.querySelector('.page-thumbnail.dragging');
    if (dragged) pageGrid.appendChild(dragged);
  });
  pageGrid.addEventListener('drop', (e) => {
    if (e.target !== pageGrid || state.dragSrcIndex == null) return;
    e.preventDefault();
    commitReorder();
  });

  // Both of these render translated text on first call, so wait for the
  // locale dictionary to be ready rather than racing it.
  (async () => {
    await window.I18N.ready;
    pollScannerStatus();
    setInterval(pollScannerStatus, 3000);
    loadJobs();
  })();
})();
