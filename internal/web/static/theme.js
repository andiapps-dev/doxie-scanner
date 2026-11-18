// Colour mode: auto (follow the system), light or dark. Loaded in <head>,
// before the page paints, so the saved choice is in place at first paint and
// there is no flash of the wrong theme. The UI that changes it lives in app.js.
(() => {
  const MODES = ['auto', 'light', 'dark'];
  const root = document.documentElement;
  const system = window.matchMedia('(prefers-color-scheme: dark)');

  // Storage can be blocked (private windows, site data off): never fail on it.
  const read = (key, allowed, fallback) => {
    try {
      const v = localStorage.getItem(key);
      return allowed.includes(v) ? v : fallback;
    } catch (e) {
      return fallback;
    }
  };
  const write = (key, value) => {
    try {
      localStorage.setItem(key, value);
    } catch (e) { /* the choice simply isn't remembered */ }
  };

  let mode = read('doxie-mode', MODES, 'auto');

  function apply() {
    const dark = mode === 'dark' || (mode === 'auto' && system.matches);
    root.setAttribute('data-bs-theme', dark ? 'dark' : 'light');
  }

  // In auto mode, follow the system as it changes (a sunset schedule, say).
  system.addEventListener('change', () => {
    if (mode === 'auto') apply();
  });
  apply();

  window.DoxieTheme = {
    MODES,
    get mode() { return mode; },
    setMode(m) {
      if (!MODES.includes(m)) return;
      mode = m;
      write('doxie-mode', m);
      apply();
    },
  };
})();
