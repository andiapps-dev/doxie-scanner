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
    scanPollTimer: null,
    cropper: null,
    // Bumped on every reload so image URLs change and the browser can't
    // serve a stale cached copy of a page that was just rotated/cropped.
    renderNonce: 0,
    // {jobId, page} for whichever page is open in the modal.
    viewer: null,
    busyDepth: 0,
    scannerConnected: false,
    scanning: false,
    // The one delete that is waiting out its Undo window (see scheduleDelete).
    pendingDelete: null,
    // What the send form was opened for: {pages, fromBar}.
    send: null,
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

  // ---- Toasts and undo ----

  // toast shows a short message at the top of the page that goes away by
  // itself. kind is a Bootstrap colour (success, danger, warning, info);
  // action ({label, onClick}) adds a button, as "Undo" does. It returns the
  // Toast so a caller can hide it early.
  function toast(message, { kind = 'success', action = null, duration = 4000 } = {}) {
    const node = document.createElement('div');
    node.className = `toast align-items-center text-bg-${kind} border-0`;
    node.setAttribute('role', 'status');
    node.innerHTML = `
      <div class="d-flex">
        <div class="toast-body"></div>
        ${action ? '<button type="button" class="btn btn-sm btn-light my-auto me-2 toast-action"></button>' : ''}
        <button type="button" class="btn-close btn-close-white me-2 m-auto" data-bs-dismiss="toast" aria-label="Close"></button>
      </div>`;
    node.querySelector('.toast-body').textContent = message;
    if (action) {
      const btn = node.querySelector('.toast-action');
      btn.textContent = action.label;
      btn.addEventListener('click', () => {
        action.onClick();
        t.hide();
      });
    }
    el('toast-area').appendChild(node);
    const t = new bootstrap.Toast(node, { delay: duration });
    node.addEventListener('hidden.bs.toast', () => node.remove());
    t.show();
    return t;
  }

  // The window to undo a delete in, in milliseconds. A test can shorten it.
  const undoMs = () => window.DOXIE_UNDO_MS ?? 8000;

  // scheduleDelete makes a delete undoable. The thing is hidden at once
  // (hide) and a toast offers Undo; only when that runs out does commit make
  // the request that really deletes it. Closing the page in the meantime
  // loses the delete, which is the safe way round. Only one delete waits at
  // a time: starting another commits the first straight away, so the page
  // numbers a later request names are never ones an earlier one shifted.
  async function scheduleDelete({ message, hide, restore, commit, target }) {
    await flushPendingDelete();
    const pending = { target, restore, commit, timer: null, toast: null, settled: false };
    state.pendingDelete = pending;
    hide();
    pending.toast = toast(message, {
      kind: 'dark',
      duration: undoMs(),
      action: {
        label: I18N.t('toast.undo'),
        onClick: () => {
          if (pending.settled) return;
          pending.settled = true;
          clearTimeout(pending.timer);
          state.pendingDelete = null;
          restore();
        },
      },
    });
    pending.timer = setTimeout(() => settlePending(pending), undoMs());
  }

  async function settlePending(pending) {
    if (pending.settled) return;
    pending.settled = true;
    clearTimeout(pending.timer);
    if (state.pendingDelete === pending) state.pendingDelete = null;
    try {
      await pending.commit();
    } catch (e) {
      toast(e.message, { kind: 'danger' });
      pending.restore();
    }
  }

  function flushPendingDelete() {
    const pending = state.pendingDelete;
    if (!pending) return Promise.resolve();
    if (pending.toast) pending.toast.hide();
    return settlePending(pending);
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

  // The grid and the selection bar show small server-made JPEGs, not the
  // full pages, which are several megabytes each.
  function pageThumbUrl(jobId, page) {
    const params = new URLSearchParams({ v: state.renderNonce });
    return `/api/scans/${jobId}/pages/${page}/thumb?${params}`;
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
    el('job-send-btn').classList.toggle('d-none', !state.paperlessUrl);
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
        showScannerHint('');
      } else {
        badge.textContent = I18N.t('status.notConnected');
        badge.className = 'badge rounded-pill text-bg-danger';
        if (data.error?.message) console.warn(`Scanner status error (${data.error.code || 'unknown'}): ${data.error.message}`);
        badge.title = data.error ? translateErrorCode(data.error.code) : '';
        const known = ['device_not_found', 'claim_interface_failed'];
        showScannerHint(I18N.t(`scan.hint.${known.includes(data.error?.code) ? data.error.code : 'other'}`));
      }
    } catch (e) {
      state.scannerConnected = false;
      badge.textContent = I18N.t('status.unknown');
      badge.className = 'badge rounded-pill text-bg-secondary';
      showScannerHint(I18N.t('scan.hint.unreachable'));
    }
    updateStartButtonState();
  }

  // A line under the new-scan controls that says what to do when the
  // scanner can't be reached, since the badge alone only says that it can't.
  function showScannerHint(text) {
    const hint = el('scanner-hint');
    hint.textContent = text;
    hint.classList.toggle('d-none', !text);
  }

  // ---- Scan jobs ----

  // Scans and pages that have been deleted but are still waiting out their
  // Undo window are hidden from every list, so reloading from the server in
  // that time can't bring one back on screen.
  function visibleJobs() {
    const hidden = state.pendingDelete && state.pendingDelete.target && state.pendingDelete.target.scans;
    return hidden ? state.jobs.filter((j) => !hidden.has(j.id)) : state.jobs;
  }

  function pendingHiddenPage(jobId, page) {
    const t = state.pendingDelete && state.pendingDelete.target;
    return !!t && t.job === jobId && t.page === page;
  }

  async function loadJobs() {
    const res = await api('/api/scans');
    state.jobs = await res.json();
    renderJobList();
    // With nothing open, the empty pane's wording depends on whether there
    // are any scans at all (load paper, or pick one).
    if (!state.selectedJob) renderJobDetail();
  }

  function renderJobList() {
    const list = el('job-list');
    list.innerHTML = '';
    const jobs = visibleJobs();
    // Nothing to select while there are no scans.
    el('bulk-toggle-btn').classList.toggle('d-none', jobs.length === 0);
    if (jobs.length === 0) {
      list.innerHTML = `<div class="list-group-item text-muted">${I18N.t('jobs.empty')}</div>`;
      return;
    }
    for (const job of jobs) {
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
      const spinner = job.status === 'running' ? '<span class="spinner-border spinner-border-sm ms-2 mt-1 flex-shrink-0" aria-hidden="true"></span>' : '';
      item.innerHTML = `
        <span>
          <div>${escapeHtml(job.name)}</div>
          <small class="job-meta ${isActive ? '' : 'text-muted'}">${escapeHtml(jobMetaLine(job))}</small>
        </span>${spinner}
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
    return `${when} · ${I18N.t('jobs.pageCount', { count: job.pageCount })} · ${I18N.t(`status.${job.status}`)}`;
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
    const selectable = visibleJobs().filter(bulkSelectable);
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
      state.bulkSelected = new Set(visibleJobs().filter(bulkSelectable).map((j) => j.id));
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
    let message = I18N.t('confirm.bulkDelete', {
      scans: I18N.t('count.scan', { count: chosen.length }),
      pages: I18N.t('count.page', { count: pages }),
    });
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
      if (deleted.length > 0) toast(I18N.t('toast.scansDeleted', { count: deleted.length }));
      if (skipped.length > 0) {
        const running = skipped.filter((x) => x.reason === 'scan_running').length;
        toast(I18N.t('alert.bulkSkipped', { scans: I18N.t('count.scan', { count: skipped.length }), running }), { kind: 'warning', duration: 7000 });
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
    // On a phone the scan opens in place of the list: start at its top.
    if (window.matchMedia('(max-width: 991.98px)').matches) window.scrollTo(0, 0);
  }

  function renderJobDetail() {
    const job = state.selectedJob;
    // On a phone only one of the list and the open scan shows at a time.
    document.body.classList.toggle('job-open', !!job);
    if (!job) {
      el('job-detail-empty').textContent = I18N.t(visibleJobs().length === 0 ? 'jobs.emptyPrompt' : 'jobs.selectPrompt');
      el('job-detail-empty').classList.remove('d-none');
      el('job-detail').classList.add('d-none');
      return;
    }
    el('job-detail-empty').classList.add('d-none');
    el('job-detail').classList.remove('d-none');
    // Don't overwrite a name that is being typed: this runs again whenever
    // the scan is reloaded, including by the Paperless status refresh.
    const nameInput = el('job-name-input');
    if (document.activeElement !== nameInput) nameInput.value = job.name;
    updateRenameButton();
    renderSentList(job);

    const grid = el('page-grid');
    grid.innerHTML = '';
    const pages = (job.pages || []).filter((p) => !pendingHiddenPage(job.id, p.index));
    updateGridHeader(job.id, pages);
    if (pages.length === 0) {
      grid.innerHTML = `<p class="text-muted">${I18N.t('jobDetail.noPages')}</p>`;
      return;
    }
    for (const page of pages) {
      grid.appendChild(renderPageThumbnail(job.id, page));
    }
  }

  // Select all / Clear above the page grid: the former while some of this
  // scan's pages are not selected, the latter while any is.
  function updateGridHeader(jobId, pages) {
    const selected = pages.filter((p) => state.combineSelection.has(combineKey(jobId, p.index))).length;
    el('grid-select-all').classList.toggle('d-none', pages.length === 0 || selected === pages.length);
    el('grid-select-none').classList.toggle('d-none', selected === 0);
  }

  function selectAllPages() {
    const job = state.selectedJob;
    if (!job) return;
    for (const p of job.pages || []) {
      if (pendingHiddenPage(job.id, p.index)) continue;
      const key = combineKey(job.id, p.index);
      if (!state.combineSelection.has(key)) {
        state.combineSelection.set(key, { jobId: job.id, page: p.index });
        state.combineOrder.push(key);
      }
    }
    renderJobDetail();
    renderCombineBar();
  }

  function clearScanSelection() {
    const job = state.selectedJob;
    if (!job) return;
    state.combineOrder = state.combineOrder.filter((k) => {
      const entry = state.combineSelection.get(k);
      if (entry && entry.jobId === job.id) {
        state.combineSelection.delete(k);
        return false;
      }
      return true;
    });
    renderJobDetail();
    renderCombineBar();
  }

  // The Save button beside the scan's name shows only while the name has
  // been changed, so the header isn't carrying a second button for nothing.
  function updateRenameButton() {
    const job = state.selectedJob;
    const typed = el('job-name-input').value.trim();
    el('job-rename-btn').classList.toggle('d-none', !job || !typed || typed === job.name);
  }

  // renderPageThumbnail renders one thumbnail per page. Duplex scanning
  // produces two independent pages per physical sheet (see
  // storage.PageMeta) — there's nothing special about a "back" page here,
  // it's just the next page in the list, same as any other.
  function renderPageThumbnail(jobId, page) {
    const thumbnail = document.createElement('div');
    thumbnail.className = 'page-thumbnail';
    thumbnail.dataset.page = page.index;

    const key = combineKey(jobId, page.index);
    const selected = state.combineSelection.has(key);
    const src = pageThumbUrl(jobId, page.index);
    const label = I18N.t('page.label', { n: page.index });
    const badges = [];
    if (page.autoCrop) badges.push(`✂ ${I18N.t('page.autoCroppedBadge')}`);
    if (page.autoRotated) badges.push(`↻ ${I18N.t('page.autoRotatedBadge')}`);
    if (sentPageSet(state.selectedJob).has(page.index)) badges.push(`✓ ${I18N.t('page.sentBadge')}`);

    thumbnail.innerHTML = `
      <img class="page-thumb" src="${src}" alt="${label}" loading="lazy" draggable="false">
      <div class="page-thumbnail-overlay">
        <input type="checkbox" class="form-check-input combine-check" ${selected ? 'checked' : ''} title="${I18N.t('modal.selectForCombine')}">
      </div>
      <div class="page-thumbnail-footer">${label}${badges.length ? `<div class="page-thumbnail-badges">${badges.map(escapeHtml).join(' · ')}</div>` : ''}</div>
    `;

    thumbnail.querySelector('.page-thumb').addEventListener('click', () => openPageViewer(jobId, page.index));
    thumbnail.querySelector('.combine-check').addEventListener('change', () => {
      toggleCombine(jobId, page.index);
    });

    return thumbnail;
  }

  // ---- Drag to reorder: mouse, finger and pen ----

  // makeSortable lets the items in container be dragged into a new order.
  // It uses Pointer Events, so one implementation covers a mouse, a finger
  // and a pen. (The browser's own HTML5 drag and drop, which this replaces,
  // does not work on phones and tablets at all.)
  //
  // A mouse starts a drag after moving a few pixels. A finger must press and
  // hold first (HOLD_MS), because a quick swipe has to keep scrolling the
  // list: a touch that moves before the hold is up is left to scroll. While
  // dragging, the item itself is moved in the DOM to where it would land, a
  // copy follows the pointer, and the container scrolls when the pointer
  // nears its edge. Releasing commits (onCommit) if the order changed;
  // Escape or a cancelled pointer puts everything back.
  const HOLD_MS = 350;
  const START_PX = 6;
  const SLOP_PX = 10;
  const EDGE_PX = 56;

  function makeSortable(container, itemSel, { onCommit }) {
    let pending = null; // the pointer is down on an item, not dragging yet
    let drag = null; // a drag is under way
    let suppressClick = false;
    let lastPointerType = 'mouse';
    const NOT_DRAG_START = 'input, button, a, label, select, textarea';

    const items = () => Array.from(container.querySelectorAll(itemSel));

    function clearPending() {
      if (pending && pending.timer) clearTimeout(pending.timer);
      pending = null;
    }

    function begin() {
      if (!pending) return;
      const { item, id, x, y, type } = pending;
      clearPending();
      const box = item.getBoundingClientRect();
      const ghost = item.cloneNode(true);
      ghost.classList.add('drag-ghost');
      ghost.style.width = `${box.width}px`;
      document.body.appendChild(ghost);
      drag = { item, ghost, id, x, y, offX: x - box.left, offY: y - box.top, startOrder: items(), raf: 0 };
      item.classList.add('dragging');
      container.classList.add('sorting');
      try { container.setPointerCapture(id); } catch (e) { /* the pointer may already be gone */ }
      if (type !== 'mouse' && navigator.vibrate) navigator.vibrate(12);
      place();
      drag.raf = requestAnimationFrame(autoScroll);
    }

    // Follow the pointer with the copy, and move the item to where it would
    // land: before or after the item under the pointer, or to the end when
    // the pointer is in the empty space after the last one.
    function place() {
      const { ghost, item, x, y, offX, offY } = drag;
      ghost.style.left = `${x - offX}px`;
      ghost.style.top = `${y - offY}px`;
      const under = document.elementFromPoint(x, y);
      const target = under && under.closest(itemSel);
      if (target && target !== item && container.contains(target)) {
        const r = target.getBoundingClientRect();
        const before = x < r.left + r.width / 2;
        const ref = before ? target : target.nextSibling;
        if (ref !== item && !(before ? target.previousSibling === item : target.nextSibling === item)) {
          container.insertBefore(item, ref);
        }
        return;
      }
      if (!target && under && (under === container || container.contains(under))) {
        const all = items();
        const last = all[all.length - 1];
        if (last && last !== item) {
          const r = last.getBoundingClientRect();
          if (y > r.bottom || (y >= r.top && x > r.right)) container.appendChild(item);
        }
      }
    }

    function autoScroll() {
      if (!drag) return;
      const r = container.getBoundingClientRect();
      const speed = (near) => Math.ceil(Math.min(1, near / EDGE_PX) * 16);
      if (container.scrollHeight > container.clientHeight) {
        if (drag.y < r.top + EDGE_PX) container.scrollTop -= speed(r.top + EDGE_PX - drag.y);
        else if (drag.y > r.bottom - EDGE_PX) container.scrollTop += speed(drag.y - (r.bottom - EDGE_PX));
      }
      if (container.scrollWidth > container.clientWidth) {
        if (drag.x < r.left + EDGE_PX) container.scrollLeft -= speed(r.left + EDGE_PX - drag.x);
        else if (drag.x > r.right - EDGE_PX) container.scrollLeft += speed(drag.x - (r.right - EDGE_PX));
      }
      place(); // what is under the pointer changes as the list scrolls
      drag.raf = requestAnimationFrame(autoScroll);
    }

    function finish(commit) {
      if (!drag) return;
      const { item, ghost, id, startOrder, raf } = drag;
      cancelAnimationFrame(raf);
      ghost.remove();
      item.classList.remove('dragging');
      container.classList.remove('sorting');
      try { container.releasePointerCapture(id); } catch (e) { /* already released */ }
      const changed = items().some((n, i) => n !== startOrder[i]);
      if (!commit) startOrder.forEach((n) => container.appendChild(n)); // put it all back
      drag = null;
      // The click that follows letting go must not also open the page.
      suppressClick = true;
      setTimeout(() => { suppressClick = false; }, 60);
      if (commit && changed) onCommit();
    }

    container.addEventListener('pointerdown', (e) => {
      lastPointerType = e.pointerType;
      if (!e.isPrimary || e.button !== 0 || drag || pending) return;
      const item = e.target.closest(itemSel);
      if (!item || !container.contains(item) || e.target.closest(NOT_DRAG_START)) return;
      pending = { item, id: e.pointerId, x0: e.clientX, y0: e.clientY, x: e.clientX, y: e.clientY, type: e.pointerType, timer: null };
      if (e.pointerType !== 'mouse') pending.timer = setTimeout(begin, HOLD_MS);
    });

    document.addEventListener('pointermove', (e) => {
      if (pending && e.pointerId === pending.id) {
        pending.x = e.clientX;
        pending.y = e.clientY;
        const moved = Math.hypot(e.clientX - pending.x0, e.clientY - pending.y0);
        if (pending.type === 'mouse') {
          if (moved > START_PX) begin();
        } else if (moved > SLOP_PX) {
          clearPending(); // a swipe: let it scroll
        }
      }
      if (drag && e.pointerId === drag.id) {
        drag.x = e.clientX;
        drag.y = e.clientY;
        place();
        e.preventDefault();
      }
    });

    document.addEventListener('pointerup', (e) => {
      if (pending && e.pointerId === pending.id) clearPending();
      if (drag && e.pointerId === drag.id) finish(true);
    });
    document.addEventListener('pointercancel', (e) => {
      if (pending && e.pointerId === pending.id) clearPending();
      if (drag && e.pointerId === drag.id) finish(false);
    });
    document.addEventListener('keydown', (e) => {
      if (drag && e.key === 'Escape') finish(false);
    });
    // Once a drag has begun, the page must not scroll under the finger.
    document.addEventListener('touchmove', (e) => { if (drag) e.preventDefault(); }, { passive: false });
    // A long press would otherwise raise the browser's image menu; a native
    // image drag would take the pointer away from us.
    container.addEventListener('contextmenu', (e) => {
      if (pending || drag || lastPointerType !== 'mouse') e.preventDefault();
    });
    container.addEventListener('dragstart', (e) => e.preventDefault());
    container.addEventListener('click', (e) => {
      if (suppressClick) {
        e.stopPropagation();
        e.preventDefault();
        suppressClick = false;
      }
    }, true);
  }

  // commitReorder reads whatever order the page thumbnails are currently
  // in (already rearranged live while dragging) and persists it: the grid
  // itself is the source of truth for the drop, not a recomputed position.
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
    if (state.selectedJob) updateGridHeader(state.selectedJob.id, (state.selectedJob.pages || []).filter((p) => !pendingHiddenPage(state.selectedJob.id, p.index)));
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
    thumbnail.dataset.key = key;

    const src = pageThumbUrl(entry.jobId, entry.page);
    thumbnail.innerHTML = `
      <img src="${src}" alt="${I18N.t('combine.thumbAlt', { n: position })}" draggable="false">
      <button type="button" class="combine-thumb-remove" title="${I18N.t('combine.removeTitle')}">&times;</button>
      <div class="combine-thumb-label">${position}</div>
    `;

    thumbnail.querySelector('img').addEventListener('click', () => {
      openPageViewer(entry.jobId, entry.page, { viewOnly: true });
    });

    thumbnail.querySelector('.combine-thumb-remove').addEventListener('click', () => {
      removeFromCombine(key);
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
    renderViewerState(jobId, page, meta);
    updateViewerNav();
  }

  // Under the viewer's title: what has been done to this page, so it
  // doesn't have to be guessed from the picture.
  function renderViewerState(jobId, page, meta) {
    const chips = [];
    if (meta && meta.autoCrop) chips.push(`✂ ${I18N.t('page.autoCroppedBadge')}`);
    if (meta && meta.autoRotated) chips.push(`↻ ${I18N.t('page.autoRotatedBadge')}`);
    if (state.selectedJob && state.selectedJob.id === jobId && sentPageSet(state.selectedJob).has(page)) {
      chips.push(`✓ ${I18N.t('page.sentBadge')}`);
    }
    el('pm-state').innerHTML = chips.map((c) => `<span class="badge text-bg-light border">${escapeHtml(c)}</span>`).join(' ');
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
    el('pm-move-earlier').disabled = busy || pos <= 0;
    el('pm-move-later').disabled = busy || pos < 0 || pos >= pages.length - 1;
    for (const [id, target] of [['pm-prev', pos - 1], ['pm-next', pos + 1]]) {
      const btn = el(id);
      btn.classList.toggle('invisible', !show);
      btn.disabled = busy || target < 0 || target >= pages.length;
    }
  }

  // Move the page on screen one place earlier or later in the scan: the
  // precise way to reorder, beside dragging, and the one that needs no
  // fingers or pointer skill.
  async function moveCurrentPage(delta) {
    const job = state.selectedJob;
    if (!job || !state.viewer || viewerBusyWithPage()) return;
    const order = (job.pages || []).map((p) => p.index);
    const pos = order.indexOf(state.viewer.page);
    const to = pos + delta;
    if (pos < 0 || to < 0 || to >= order.length) return;
    [order[pos], order[to]] = [order[to], order[pos]];
    await withBusy(async () => {
      await api(`/api/scans/${job.id}/pages/order`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ order }),
      });
      await selectJob(job.id);
      loadViewerImage();
    });
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

  // dropFromSelection takes one page out of the selection bar.
  function dropFromSelection(jobId, page) {
    const key = combineKey(jobId, page);
    state.combineSelection.delete(key);
    state.combineOrder = state.combineOrder.filter((k) => k !== key);
  }

  // Deleting a page hides it at once and offers Undo; the page is only
  // really deleted when the Undo window has passed (see scheduleDelete).
  async function deleteCurrentPage() {
    if (!state.viewer) return;
    const { jobId, page } = state.viewer;
    // The server renumbers the remaining pages by their position, so the
    // selection (kept by page number) must follow once it has, or "send" and
    // "combine" would pick the wrong pages afterwards.
    const remaining = ((state.selectedJob && state.selectedJob.id === jobId && state.selectedJob.pages) || [])
      .filter((p) => p.index !== page);
    const newIndex = new Map(remaining.map((p, i) => [p.index, i + 1]));
    pageModal().hide();
    await scheduleDelete({
      message: I18N.t('toast.pageDeleted'),
      target: { job: jobId, page },
      hide: () => {
        dropFromSelection(jobId, page);
        renderJobDetail();
        renderCombineBar();
      },
      restore: () => renderJobDetail(),
      commit: async () => {
        await api(`/api/scans/${jobId}/pages/${page}`, { method: 'DELETE' });
        renumberSelection(jobId, newIndex);
        renderCombineBar();
        if (state.selectedJobId === jobId) await selectJob(jobId);
        await loadJobs();
      },
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

  async function exportWholeScan(format = 'jpeg') {
    const job = state.selectedJob;
    if (!job || !job.pages || job.pages.length === 0) {
      toast(I18N.t('alert.noPages'), { kind: 'warning' });
      return;
    }
    const pages = job.pages.map((p) => ({ jobId: job.id, page: p.index }));
    await downloadCombinedPDF(pages, job.name, format);
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
      if (turned) toast(I18N.t('jobDetail.autoRotated', { count: turned }));
      else toast(I18N.t('jobDetail.noneRotated'), { kind: 'info' });
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
      if (cropped) toast(I18N.t('jobDetail.autoCropped', { count: cropped }));
      else toast(I18N.t('jobDetail.noneCropped'), { kind: 'info' });
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
      toast(I18N.t('toast.restored'));
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
      return `<li><strong>${escapeHtml(d.title)}</strong> <span class="text-muted">(${I18N.t('sent.pages', { count: (d.pages || []).length, pages: (d.pages || []).join(', ') })})</span> <span class="${cls}">${status}</span></li>`;
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

  // Tags are toggle chips: a multi-select list box needs Ctrl-click, which
  // is hard to do at all on a phone.
  function fillTags(container, items) {
    container.innerHTML = '';
    for (const it of items) {
      const input = document.createElement('input');
      input.type = 'checkbox';
      input.className = 'btn-check';
      input.id = `send-tag-${it.id}`;
      input.value = String(it.id);
      input.autocomplete = 'off';
      const label = document.createElement('label');
      label.className = 'btn btn-sm btn-outline-primary';
      label.htmlFor = input.id;
      label.textContent = it.name;
      container.append(input, label);
    }
  }

  // openSendDialog opens the send form for pages: by default the selection
  // bar's pages, or (from a scan's own button) that scan's pages. The pages
  // are kept in state.send so the form sends what it was opened for, and
  // only a send from the selection bar clears that selection afterwards.
  async function openSendDialog(pages = selectedPagesInOrder(), title = el('combine-title').value.trim(), fromBar = true) {
    if (pages.length === 0) return;
    state.send = { pages, fromBar };
    el('send-error').classList.add('d-none');
    el('send-summary').textContent = I18N.t('send.summary', { count: pages.length });
    el('send-title').value = title;
    el('send-created').value = '';
    try {
      await withBusy(async () => {
        const res = await api('/api/paperless/metadata');
        const m = await res.json();
        const none = I18N.t('send.letPaperless');
        fillSelect(el('send-correspondent'), m.correspondents || [], none);
        fillSelect(el('send-doctype'), m.documentTypes || [], none);
        fillTags(el('send-tags'), m.tags || []);
      });
    } catch (e) {
      toast(`${I18N.t('send.loadFailed')} ${e.message}`, { kind: 'danger', duration: 7000 });
      return;
    }
    bootstrap.Modal.getOrCreateInstance(el('send-modal')).show();
  }

  // Sends the open scan from its own button. Pages not yet in Paperless are
  // the ones to send; if all of them are already there, all of them.
  function sendScanToPaperless() {
    const job = state.selectedJob;
    if (!job || !(job.pages || []).length) return;
    const sent = sentPageSet(job);
    let pick = job.pages.filter((p) => !sent.has(p.index));
    if (pick.length === 0) pick = job.pages;
    return openSendDialog(pick.map((p) => ({ jobId: job.id, page: p.index })), job.name, false);
  }

  async function submitSend() {
    const { pages, fromBar } = state.send;
    const tags = Array.from(el('send-tags').querySelectorAll('input:checked')).map((i) => Number(i.value));
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
    const sentAs = body.title || (state.jobs.find((j) => j.id === pages[0].jobId) || {}).name || '';
    toast(I18N.t('toast.sent', { count: pages.length, title: sentAs }));
    if (fromBar) {
      state.combineSelection.clear();
      state.combineOrder = [];
      renderCombineBar();
    }
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
    document.body.classList.add('scanning');
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
      toast(e.message, { kind: 'danger', duration: 7000 });
      state.scanning = false;
      document.body.classList.remove('scanning');
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
    let tick = 0;
    state.scanPollTimer = setInterval(async () => {
      const res = await api(`/api/scans/${jobId}`);
      const job = await res.json();
      if (job.status === 'running') {
        // The running scan shows up in the list (with a spinner) a few
        // seconds in, rather than only once it has finished.
        if (++tick % 4 === 1) loadJobs().catch(() => {});
        const counted = (job.stage === 'rotating' || job.stage === 'cropping') && job.stageTotal > 0;
        showScanProgress(scanProgressText(job), counted ? (job.stageDone - 1) / job.stageTotal : null);
        return;
      }
      clearInterval(state.scanPollTimer);
      state.scanPollTimer = null;
      state.scanning = false;
      document.body.classList.remove('scanning');
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
    if (state.selectedJob) state.selectedJob.name = name;
    updateRenameButton();
    await loadJobs();
  }

  // Deleting a scan hides it at once and offers Undo; it is only really
  // deleted when the Undo window has passed (see scheduleDelete).
  async function deleteJob() {
    const id = state.selectedJobId;
    if (!id) return;
    await scheduleDelete({
      message: I18N.t('toast.scanDeleted'),
      target: { scans: new Set([id]) },
      hide: () => {
        forgetScans(new Set([id]));
        renderJobList();
      },
      restore: () => {
        renderJobList();
        renderJobDetail();
      },
      commit: async () => {
        await api(`/api/scans/${id}`, { method: 'DELETE' });
        await loadJobs();
      },
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
  el('combine-send-btn').addEventListener('click', () => openSendDialog());
  el('job-send-btn').addEventListener('click', sendScanToPaperless);
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
  el('job-name-input').addEventListener('input', updateRenameButton);
  el('job-name-input').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); renameJob(); }
    if (e.key === 'Escape' && state.selectedJob) { el('job-name-input').value = state.selectedJob.name; updateRenameButton(); }
  });
  el('job-delete-btn').addEventListener('click', deleteJob);
  el('job-export-pdf-btn').addEventListener('click', () => exportWholeScan('jpeg'));
  document.querySelectorAll('.job-export-fmt').forEach((b) => b.addEventListener('click', () => exportWholeScan(b.dataset.fmt)));
  el('job-back-btn').addEventListener('click', async () => {
    await flushPendingDelete();
    state.selectedJobId = null;
    state.selectedJob = null;
    renderJobList();
    renderJobDetail();
    window.scrollTo(0, 0);
  });
  el('grid-select-all').addEventListener('click', selectAllPages);
  el('grid-select-none').addEventListener('click', clearScanSelection);
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
  el('pm-move-earlier').addEventListener('click', () => moveCurrentPage(-1));
  el('pm-move-later').addEventListener('click', () => moveCurrentPage(1));
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

  // The page grid and the selection bar are never recreated, only their
  // children, so each sorter is attached once.
  makeSortable(el('page-grid'), '.page-thumbnail', { onCommit: commitReorder });
  makeSortable(el('combine-thumbs'), '.combine-thumb', {
    onCommit: () => {
      // The bar's order is what "Combine" and "Send" use, in this order.
      state.combineOrder = Array.from(el('combine-thumbs').querySelectorAll('.combine-thumb')).map((t) => t.dataset.key);
      renderCombineBar();
    },
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
