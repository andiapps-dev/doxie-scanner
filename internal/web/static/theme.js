// Colour mode and colour scheme. Loaded in <head>, before the page paints, so
// the saved choice is in place at first paint and there is no flash of the
// wrong theme. The UI that changes them lives in app.js.
//
// Mode:   auto (follow the system's light/dark setting), light, or dark.
// Scheme: the accent colour (slate is the original look); see style.css.
(() => {
  const MODES = ['auto', 'light', 'dark'];
  const SCHEMES = ['slate', 'ocean', 'forest', 'violet', 'rose'];
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
  let scheme = read('doxie-scheme', SCHEMES, 'slate');

  function apply() {
    const dark = mode === 'dark' || (mode === 'auto' && system.matches);
    root.setAttribute('data-bs-theme', dark ? 'dark' : 'light');
    root.setAttribute('data-scheme', scheme);
  }

  // In auto mode, follow the system as it changes (a sunset schedule, say).
  system.addEventListener('change', () => {
    if (mode === 'auto') apply();
  });
  apply();

  window.DoxieTheme = {
    MODES,
    SCHEMES,
    get mode() { return mode; },
    get scheme() { return scheme; },
    setMode(m) {
      if (!MODES.includes(m)) return;
      mode = m;
      write('doxie-mode', m);
      apply();
    },
    setScheme(s) {
      if (!SCHEMES.includes(s)) return;
      scheme = s;
      write('doxie-scheme', s);
      apply();
    },
  };
})();
