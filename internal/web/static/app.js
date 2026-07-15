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

  async function api(path, opts) {
    const res = await fetch(path, opts);
    if (!res.ok && res.headers.get('content-type')?.includes('application/json')) {
      const body = await res.json().catch(() => null);
      const msg = body?.error?.message || `request failed (${res.status})`;
      throw new Error(msg);
    }
    if (!res.ok) {
      throw new Error(`request failed (${res.status})`);
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
      btn.title = 'A scan is already in progress';
    } else if (!state.scannerConnected) {
      btn.title = 'Waiting for scanner connection…';
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
        badge.textContent = 'Scanner connected';
        badge.className = 'badge rounded-pill text-bg-success';
        badge.title = '';
      } else {
        badge.textContent = 'Scanner not connected';
        badge.className = 'badge rounded-pill text-bg-danger';
        badge.title = data.error?.message || '';
      }
    } catch (e) {
      state.scannerConnected = false;
      badge.textContent = 'Status unknown';
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
      list.innerHTML = '<div class="list-group-item text-muted">No scans yet.</div>';
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
      item.innerHTML = `
        <span>
          <div>${escapeHtml(job.name)}</div>
          <small class="job-meta ${isActive ? '' : 'text-muted'}">${escapeHtml(when)} · ${job.pageCount} page(s) · ${escapeHtml(job.status)}</small>
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
      grid.innerHTML = '<p class="text-muted">No pages yet.</p>';
      return;
    }
    for (const page of pages) {
      grid.appendChild(renderPageTile(job.id, page));
    }
  }

  // renderPageTile renders one tile per page. Duplex scanning produces two
  // independent pages per physical sheet (see storage.PageMeta) — there's
  // nothing special about a "back" page here, it's just the next page in
  // the list, same as any other.
  function renderPageTile(jobId, page) {
    const tile = document.createElement('div');
    tile.className = 'page-tile';
    tile.draggable = true;
    tile.dataset.page = page.index;

    const key = combineKey(jobId, page.index);
    const selected = state.combineSelection.has(key);
    const src = pageImageUrl(jobId, page.index);
    const label = `Page ${page.index}`;

    tile.innerHTML = `
      <img class="page-thumb" src="${src}" alt="${label}">
      <div class="page-tile-overlay">
        <input type="checkbox" class="form-check-input combine-check" ${selected ? 'checked' : ''} title="Select for combine">
      </div>
      <div class="page-tile-footer">${label}</div>
    `;

    tile.querySelector('.page-thumb').addEventListener('click', () => openPageViewer(jobId, page.index));
    tile.querySelector('.combine-check').addEventListener('change', () => {
      toggleCombine(jobId, page.index);
    });

    tile.addEventListener('dragstart', (e) => {
      state.dragSrcIndex = page.index;
      tile.classList.add('dragging');
      e.dataTransfer.effectAllowed = 'move';
    });
    tile.addEventListener('dragend', () => {
      tile.classList.remove('dragging');
      state.dragSrcIndex = null;
      // Re-render from the authoritative state: if a drop already
      // committed a reorder, selectJob() has (or will have) updated
      // state.selectedJob and this reflects that; if the drag was
      // cancelled with no drop, this simply restores the original order.
      renderJobDetail();
    });
    tile.addEventListener('dragover', (e) => {
      e.preventDefault();
      if (state.dragSrcIndex == null || state.dragSrcIndex === page.index) return;
      // Move the actual dragged tile in the DOM, live, to exactly where it
      // would land — what you see while dragging is what you get, no
      // separate indicator to interpret.
      const dragged = el('page-grid').querySelector('.page-tile.dragging');
      if (!dragged) return;
      const rect = tile.getBoundingClientRect();
      const before = e.clientX - rect.left < rect.width / 2;
      tile.parentNode.insertBefore(dragged, before ? tile : tile.nextSibling);
    });
    tile.addEventListener('drop', (e) => {
      e.preventDefault();
      if (state.dragSrcIndex == null) return;
      commitReorder();
    });

    return tile;
  }

  // commitReorder reads whatever order the page tiles are currently in
  // (already rearranged live during dragover) and persists it — the grid
  // itself is the source of truth for the drop, not a recomputed position.
  function commitReorder() {
    const jobId = state.selectedJobId;
    const order = Array.from(el('page-grid').querySelectorAll('.page-tile')).map((t) => Number(t.dataset.page));
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
  // thumbnail strip (rather than from wherever its page tile happens to
  // be, which might be a different scan entirely than the one currently
  // open).
  function removeFromCombine(key) {
    const entry = state.combineSelection.get(key);
    if (!entry) return;
    toggleCombine(entry.jobId, entry.page);
  }

  function renderCombineBar() {
    const bar = el('combine-bar');
    const count = state.combineOrder.length;
    el('combine-count').textContent = count;
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
    const tile = document.createElement('div');
    tile.className = 'combine-thumb';
    tile.draggable = true;
    tile.dataset.key = key;

    const src = pageImageUrl(entry.jobId, entry.page);
    tile.innerHTML = `
      <img src="${src}" alt="Page ${position} of combined document">
      <button type="button" class="combine-thumb-remove" title="Remove from selection">&times;</button>
      <div class="combine-thumb-label">${position}</div>
    `;

    tile.querySelector('img').addEventListener('click', () => {
      openPageViewer(entry.jobId, entry.page, { viewOnly: true });
    });

    tile.querySelector('.combine-thumb-remove').addEventListener('click', () => {
      removeFromCombine(key);
    });

    tile.addEventListener('dragstart', (e) => {
      state.combineDragKey = key;
      tile.classList.add('dragging');
      e.dataTransfer.effectAllowed = 'move';
    });
    tile.addEventListener('dragend', () => {
      tile.classList.remove('dragging');
      state.combineDragKey = null;
      renderCombineBar();
    });
    tile.addEventListener('dragover', (e) => {
      e.preventDefault();
      if (state.combineDragKey == null || state.combineDragKey === key) return;
      const dragged = el('combine-thumbs').querySelector('.combine-thumb.dragging');
      if (!dragged) return;
      const rect = tile.getBoundingClientRect();
      const before = e.clientX - rect.left < rect.width / 2;
      tile.parentNode.insertBefore(dragged, before ? tile : tile.nextSibling);
    });
    tile.addEventListener('drop', (e) => {
      e.preventDefault();
      if (state.combineDragKey == null) return;
      // Commit the order directly from the live DOM arrangement — what
      // you saw while dragging is what gets combined.
      state.combineOrder = Array.from(el('combine-thumbs').querySelectorAll('.combine-thumb')).map((t) => t.dataset.key);
    });

    return tile;
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
    el('page-modal-title').textContent = `Page ${page}`;
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

  async function deleteCurrentPage() {
    if (!state.viewer) return;
    const { jobId, page } = state.viewer;
    if (!confirm('Delete this page?')) return;
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

  async function downloadCombinedPDF(pages, title) {
    await withBusy(async () => {
      const res = await api('/api/export/combine', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ pages, title }),
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
    await downloadCombinedPDF(pages, title);
  }

  async function exportWholeScan() {
    const job = state.selectedJob;
    if (!job || !job.pages || job.pages.length === 0) {
      alert('This scan has no pages yet.');
      return;
    }
    const pages = job.pages.map((p) => ({ jobId: job.id, page: p.index }));
    await downloadCombinedPDF(pages, job.name);
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

  function watchScanProgress(jobId) {
    el('scan-progress').classList.remove('d-none');
    if (state.scanPollTimer) clearInterval(state.scanPollTimer);
    state.scanPollTimer = setInterval(async () => {
      const res = await api(`/api/scans/${jobId}`);
      const job = await res.json();
      el('scan-progress-count').textContent = job.pagesScanned ?? job.pageCount ?? 0;
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
    if (!confirm('Delete this scan and all of its pages?')) return;
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
      const qs = new URLSearchParams({ format: a.dataset.format });
      window.open(`/api/scans/${jobId}/pages/${page}/export?${qs}`, '_blank');
    });
  });
  el('page-modal').addEventListener('hidden.bs.modal', () => {
    endCropUI();
    state.viewer = null;
  });

  // Fallback for dropping into empty grid space past the last tile (e.g.
  // an incomplete last row) — wired once here rather than in
  // renderPageTile, since #page-grid itself is never recreated, only its
  // children (renderJobDetail rebuilds those on every render).
  const pageGrid = el('page-grid');
  pageGrid.addEventListener('dragover', (e) => {
    if (e.target !== pageGrid || state.dragSrcIndex == null) return;
    e.preventDefault();
    const dragged = pageGrid.querySelector('.page-tile.dragging');
    if (dragged) pageGrid.appendChild(dragged);
  });
  pageGrid.addEventListener('drop', (e) => {
    if (e.target !== pageGrid || state.dragSrcIndex == null) return;
    e.preventDefault();
    commitReorder();
  });

  pollScannerStatus();
  setInterval(pollScannerStatus, 3000);
  loadJobs();
})();
