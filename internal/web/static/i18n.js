// Tiny, zero-dependency i18n: no build step, no bundler, matching the
// rest of this app's frontend. Locale dictionaries live in
// locales/<code>.json and are picked up automatically by the Go
// backend's recursive //go:embed static — adding a new language is
// just dropping another JSON file here, no server code changes.
(() => {
  'use strict';

  const SUPPORTED = ['en', 'es'];
  const DEFAULT_LOCALE = 'en';
  const STORAGE_KEY = 'doxie-lang';

  function resolveLocale() {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved && SUPPORTED.includes(saved)) return saved;
    for (const tag of navigator.languages || [navigator.language]) {
      const base = (tag || '').slice(0, 2).toLowerCase();
      if (SUPPORTED.includes(base)) return base;
    }
    return DEFAULT_LOCALE;
  }

  function lookup(dict, key) {
    return key.split('.').reduce((v, k) => (v && typeof v === 'object' ? v[k] : undefined), dict);
  }

  function interpolate(str, vars) {
    if (!vars) return str;
    return str.replace(/\{(\w+)\}/g, (_, name) => (name in vars ? vars[name] : `{${name}}`));
  }

  let dict = {};

  // t looks up a dotted key (e.g. "modal.rotateLeft") and substitutes any
  // {name} placeholders from vars. Falls back to the raw key itself if
  // the dictionary is missing the entry, so a translation gap is visibly
  // obvious rather than silently blank.
  function t(key, vars) {
    const value = lookup(dict, key);
    if (typeof value !== 'string') return key;
    return interpolate(value, vars);
  }

  // Applies the current dictionary to every static string in index.html
  // via data-i18n(-placeholder/-title/-alt) attributes. app.js handles
  // its own dynamically-rendered strings directly via t().
  function applyTranslations() {
    document.querySelectorAll('[data-i18n]').forEach((elm) => {
      elm.textContent = t(elm.getAttribute('data-i18n'));
    });
    document.querySelectorAll('[data-i18n-placeholder]').forEach((elm) => {
      elm.placeholder = t(elm.getAttribute('data-i18n-placeholder'));
    });
    document.querySelectorAll('[data-i18n-title]').forEach((elm) => {
      elm.title = t(elm.getAttribute('data-i18n-title'));
    });
    document.querySelectorAll('[data-i18n-alt]').forEach((elm) => {
      elm.alt = t(elm.getAttribute('data-i18n-alt'));
    });
  }

  async function loadDict(locale) {
    try {
      const res = await fetch(`locales/${locale}.json`);
      if (!res.ok) throw new Error(`failed to load locale ${locale}`);
      return await res.json();
    } catch (e) {
      if (locale !== DEFAULT_LOCALE) return loadDict(DEFAULT_LOCALE);
      throw e;
    }
  }

  // The switcher's own options ("English"/"Español") are each language's
  // native name, so they need no translation themselves. Switching
  // reloads the page — simpler and more robust than re-running every
  // dynamic render (job list, combine bar, open modal) in place.
  function setupSwitcher(locale) {
    const select = document.getElementById('lang-switcher');
    if (!select) return;
    select.value = locale;
    select.addEventListener('change', () => {
      localStorage.setItem(STORAGE_KEY, select.value);
      location.reload();
    });
  }

  const locale = resolveLocale();
  const ready = loadDict(locale).then((loaded) => {
    dict = loaded;
    document.documentElement.lang = locale;
    applyTranslations();
    setupSwitcher(locale);
  });

  window.I18N = {
    t,
    ready,
    get locale() {
      return locale;
    },
  };
})();
