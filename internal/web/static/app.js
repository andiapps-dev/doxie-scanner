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

  async function pollScannerStatus() {
    const badge = el('scanner-badge');
    try {
      const res = await api('/api/scanner/status');
      const data = await res.json();
      state.scannerConnected = data.connected;
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
      const isActive = job.id === state.selectedJobId;
      const item = document.createElement('button');
      item.type = 'button';
      item.className = 'list-group-item list-group-item-action d-flex justify-content-between align-items-start';
      if (isActive) {
        item.classList.add('active');
        item.setAttribute('aria-current', 'true');
      }
      const when = new Date(job.createdAt).toLocaleString();
      const statusText = I18N.t(`status.${job.status}`);
      item.innerHTML = `
        <span>
          <div>${escapeHtml(job.name)}</div>
          <small class="job-meta ${isActive ? '' : 'text-muted'}">${escapeHtml(when)} · ${job.pageCount} ${I18N.t('jobs.pageCountUnit')} · ${escapeHtml(statusText)}</small>
        </span>
      `;
      item.addEventListener('click', () => selectJob(job.id));
      list.appendChild(item);
    }
  }

  async function selectJob(jobId) {
    state.selectedJobId = jobId;
    state.renderNonce++;
    const res = await api(`/api/scans/${jobId}`);
    state.selectedJob = await res.json();
    renderJobList();
    renderJobDetail();
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

    thumbnail.innerHTML = `
      <img class="page-thumb" src="${src}" alt="${label}">
      <div class="page-thumbnail-overlay">
        <input type="checkbox" class="form-check-input combine-check" ${selected ? 'checked' : ''} title="${I18N.t('modal.selectForCombine')}">
      </div>
      <div class="page-thumbnail-footer">${label}</div>
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
    state.viewer = { jobId, page: pageIndex };
    loadViewerImage();
    el('page-modal-view-actions').classList.toggle('d-none', !!opts.viewOnly);
    pageModal().show();
  }

  function loadViewerImage() {
    const { jobId, page } = state.viewer;
    el('page-modal-image').src = pageImageUrl(jobId, page);
    el('page-modal-title').textContent = I18N.t('page.label', { n: page });
    el('pm-combine-check').checked = state.combineSelection.has(combineKey(jobId, page));
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
  }

  function endCropUI() {
    if (state.cropper) {
      state.cropper.destroy();
      state.cropper = null;
    }
    el('page-modal-view-actions').classList.remove('d-none');
    el('page-modal-crop-actions').classList.add('d-none');
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

  async function deleteCurrentPage() {
    if (!state.viewer) return;
    const { jobId, page } = state.viewer;
    if (!confirm(I18N.t('confirm.deletePage'))) return;
    await withBusy(async () => {
      await api(`/api/scans/${jobId}/pages/${page}`, { method: 'DELETE' });
      const key = combineKey(jobId, page);
      state.combineSelection.delete(key);
      state.combineOrder = state.combineOrder.filter((k) => k !== key);
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

  // ---- Start scan + progress polling ----

  async function startScan() {
    const duplex = el('duplex-toggle').checked;
    state.scanning = true;
    updateStartButtonState();
    try {
      const res = await api('/api/scans', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ duplex }),
      });
      const data = await res.json();
      watchScanProgress(data.jobId);
    } catch (e) {
      alert(e.message);
      state.scanning = false;
      updateStartButtonState();
    }
  }

  function updateScanProgressText(count) {
    el('scan-progress').textContent = I18N.t('scan.progressText', { count });
  }

  function watchScanProgress(jobId) {
    el('scan-progress').classList.remove('d-none');
    updateScanProgressText(0);
    if (state.scanPollTimer) clearInterval(state.scanPollTimer);
    state.scanPollTimer = setInterval(async () => {
      const res = await api(`/api/scans/${jobId}`);
      const job = await res.json();
      updateScanProgressText(job.pagesScanned ?? job.pageCount ?? 0);
      if (job.status !== 'running') {
        clearInterval(state.scanPollTimer);
        state.scanPollTimer = null;
        el('scan-progress').classList.add('d-none');
        state.scanning = false;
        updateStartButtonState();
        await loadJobs();
        await selectJob(jobId);
      }
    }, 1000);
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
      await api(`/api/scans/${state.selectedJobId}`, { method: 'DELETE' });
      state.selectedJobId = null;
      state.selectedJob = null;
      renderJobDetail();
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
