document.addEventListener('DOMContentLoaded', function () {
  initPasswordToggles();
  initConfirmPasswordMatch();
  initPasswordStrengthMeter();
  initTabs();
  initDropzones();
  recordRecentReport();
  renderRecentReports();
  initScrollReveal();
  initSidebar();
  initDashboardSections();
});

// Switches between the dashboard's sections (Overview / Analyze) without a
// page navigation — the sidebar's Dashboard/Analyze links carry both a real
// href (so they still work with JS disabled, and from other pages that
// reuse app-sidebar, like the standalone /analyze page) and
// data-dash-nav-trigger; when a matching [data-dash-panel] exists on the
// current page, clicks are intercepted and swap panels in place instead.
// No-ops on pages with no dashboard panels (the href just does its job).
function initDashboardSections() {
  var triggers = document.querySelectorAll('[data-dash-nav-trigger]');
  var panels = document.querySelectorAll('[data-dash-panel]');
  if (!triggers.length || !panels.length) return;

  triggers.forEach(function (trigger) {
    trigger.addEventListener('click', function (e) {
      e.preventDefault();
      var section = trigger.getAttribute('data-dash-nav-trigger');

      triggers.forEach(function (t) {
        var active = t.getAttribute('data-dash-nav-trigger') === section;
        t.classList.toggle('bg-brand/10', active);
        t.classList.toggle('text-brand', active);
        t.classList.toggle('font-medium', active);
        t.classList.toggle('text-gray-400', !active);
      });

      panels.forEach(function (p) {
        p.classList.toggle('hidden', p.getAttribute('data-dash-panel') !== section);
      });

      var url = section === 'analyze' ? '/dashboard?tab=analyze' : '/dashboard';
      window.history.replaceState({}, '', url);

      // Close the mobile drawer after picking a section — staying open
      // over the freshly-swapped content just gets in the way.
      if (window.matchMedia('(max-width: 639px)').matches) {
        var sidebar = document.getElementById('app-sidebar');
        var backdrop = document.getElementById('sidebar-backdrop');
        if (sidebar) sidebar.classList.add('-translate-x-full');
        if (backdrop) backdrop.classList.add('hidden');
      }
    });
  });
}

// Toggles the off-canvas app-sidebar (see the app-sidebar/app-topbar
// partials in base.html) on mobile — #sidebar-toggle lives in the topbar,
// #sidebar-backdrop closes it on outside click. No-ops on pages that don't
// have a sidebar.
function initSidebar() {
  var toggle = document.getElementById('sidebar-toggle');
  var sidebar = document.getElementById('app-sidebar');
  var backdrop = document.getElementById('sidebar-backdrop');
  if (!toggle || !sidebar) return;

  var isOpen = function () { return !sidebar.classList.contains('-translate-x-full'); };
  var open = function () {
    sidebar.classList.remove('-translate-x-full');
    if (backdrop) backdrop.classList.remove('hidden');
  };
  var close = function () {
    sidebar.classList.add('-translate-x-full');
    if (backdrop) backdrop.classList.add('hidden');
  };

  toggle.addEventListener('click', function () {
    isOpen() ? close() : open();
  });
  if (backdrop) backdrop.addEventListener('click', close);
}

// Fades/slides in any [data-reveal] element the first time it scrolls into
// view (each element reveals once, then stops being observed). Elements can
// carry data-reveal-delay="<ms>" for a staggered sequence — used by the
// landing page's product-lifecycle section so the stages animate in left to
// right instead of all appearing at once.
function initScrollReveal() {
  var items = document.querySelectorAll('[data-reveal]');
  if (!items.length) return;

  if (!('IntersectionObserver' in window)) {
    items.forEach(function (el) { el.classList.add('is-revealed'); });
    return;
  }

  var observer = new IntersectionObserver(function (entries) {
    entries.forEach(function (entry) {
      if (!entry.isIntersecting) return;
      var el = entry.target;
      var delay = el.getAttribute('data-reveal-delay');
      if (delay) {
        el.style.transitionDelay = delay + 'ms';
      }
      el.classList.add('is-revealed');
      observer.unobserve(el);
    });
  }, { threshold: 0.2 });

  items.forEach(function (el) { observer.observe(el); });
}

// --- anonymous "recent on this device" ---------------------------------
//
// No accounts, no cookies, no server-side state: just a capped list in
// localStorage. recordRecentReport() runs on the report page and only
// stores an entry when the URL has ?new=1 (set once, by the redirect that
// follows a just-completed analysis) — that's what distinguishes "I just
// created this" from "I'm viewing a link someone shared." renderRecentReports()
// runs on the analyze page and shows the list when there's one and no
// signed-in user (the section only exists in the DOM in that case).

var RECENT_REPORTS_KEY = 'deployable_recent_reports';
var RECENT_REPORTS_MAX = 10;

function readRecentReports() {
  try {
    var raw = localStorage.getItem(RECENT_REPORTS_KEY);
    return raw ? JSON.parse(raw) : [];
  } catch (e) {
    return [];
  }
}

function recordRecentReport() {
  var meta = document.getElementById('report-meta');
  if (!meta) return;

  var params = new URLSearchParams(window.location.search);
  if (params.get('new') !== '1') return;

  var entry = {
    slug: meta.getAttribute('data-slug'),
    language: meta.getAttribute('data-language') || 'Unknown stack',
    score: meta.getAttribute('data-score'),
  };

  var list = readRecentReports().filter(function (r) { return r.slug !== entry.slug; });
  list.unshift(entry);
  try {
    localStorage.setItem(RECENT_REPORTS_KEY, JSON.stringify(list.slice(0, RECENT_REPORTS_MAX)));
  } catch (e) {
    // localStorage unavailable (private browsing, quota) — recording is
    // best-effort, nothing else on the page depends on it.
  }

  params.delete('new');
  var query = params.toString();
  window.history.replaceState({}, '', window.location.pathname + (query ? '?' + query : ''));
}

function renderRecentReports() {
  var section = document.getElementById('recent-reports-section');
  var list = document.getElementById('recent-reports-list');
  if (!section || !list) return;

  var reports = readRecentReports();
  if (reports.length === 0) return;

  reports.forEach(function (r) {
    var link = document.createElement('a');
    link.href = '/report/' + encodeURIComponent(r.slug);
    link.className = 'flex items-center justify-between gap-2 rounded-md border border-editor-border bg-editor-bg px-3 py-2 text-sm hover:border-brand/40 transition-colors';

    var lang = document.createElement('span');
    lang.className = 'text-gray-300';
    lang.textContent = r.language;

    var score = document.createElement('span');
    score.className = 'text-xs text-gray-500';
    score.textContent = r.score + '/100';

    link.appendChild(lang);
    link.appendChild(score);
    list.appendChild(link);
  });

  section.classList.remove('hidden');
}

// Delegated (survives HTMX-swapped content, e.g. the generated-API-key
// partial): any [data-copy="<target id>"] button copies that element's text
// and briefly swaps its icon to a checkmark.
document.addEventListener('click', function (e) {
  var btn = e.target.closest('[data-copy]');
  if (!btn) return;
  var target = document.getElementById(btn.getAttribute('data-copy'));
  if (!target || !navigator.clipboard) return;

  navigator.clipboard.writeText(target.textContent.trim()).then(function () {
    var shown = btn.querySelector('[data-icon-shown]');
    var hidden = btn.querySelector('[data-icon-hidden]');
    if (!shown || !hidden) return;
    shown.classList.add('hidden');
    hidden.classList.remove('hidden');
    setTimeout(function () {
      shown.classList.remove('hidden');
      hidden.classList.add('hidden');
    }, 1500);
  });
});

// Simple client-side tabs: a [data-tabs] container holds [data-tab-trigger]
// buttons and [data-tab-panel] panels; clicking a trigger shows the panel
// with the matching name and hides the rest. Pure UI state — no data
// changes between tabs, so this stays client-side rather than round-tripping
// through HTMX.
function initTabs() {
  document.querySelectorAll('[data-tabs]').forEach(function (group) {
    var triggers = group.querySelectorAll('[data-tab-trigger]');
    var panels = group.querySelectorAll('[data-tab-panel]');

    triggers.forEach(function (trigger) {
      trigger.addEventListener('click', function () {
        var name = trigger.getAttribute('data-tab-trigger');

        triggers.forEach(function (t) {
          var active = t === trigger;
          t.classList.toggle('bg-brand', active);
          t.classList.toggle('text-white', active);
          t.classList.toggle('shadow-sm', active);
          t.classList.toggle('text-gray-400', !active);
          t.setAttribute('aria-selected', active ? 'true' : 'false');
        });

        panels.forEach(function (p) {
          p.classList.toggle('hidden', p.getAttribute('data-tab-panel') !== name);
        });
      });
    });
  });
}

// Drag-and-drop visual feedback + filename echo for [data-dropzone] file
// drop targets. The upload itself is still triggered by htmx
// (hx-trigger="drop"/"change" on the element) — this only handles the
// highlight state and showing which file was picked before the request
// finishes.
function initDropzones() {
  document.querySelectorAll('[data-dropzone]').forEach(function (zone) {
    var label = zone.querySelector('[data-dropzone-label]');
    var input = zone.querySelector('input[type="file"]');

    var highlight = function (on) {
      zone.classList.toggle('border-brand', on);
      zone.classList.toggle('bg-brand/5', on);
    };
    var showName = function (name) {
      if (label && name) label.textContent = name;
    };

    zone.addEventListener('dragover', function (e) {
      e.preventDefault();
      highlight(true);
    });
    zone.addEventListener('dragleave', function () {
      highlight(false);
    });
    zone.addEventListener('drop', function (e) {
      highlight(false);
      var file = e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files[0];
      if (file) showName(file.name);
    });

    if (input) {
      input.addEventListener('change', function () {
        if (input.files && input.files[0]) showName(input.files[0].name);
      });
    }
  });
}

// Any button with data-password-toggle="<input id>" swaps that input between
// type="password" and type="text", and toggles its own eye / eye-off icons
// (two <svg data-icon-shown/data-icon-hidden> children).
function initPasswordToggles() {
  document.querySelectorAll('[data-password-toggle]').forEach(function (btn) {
    var input = document.getElementById(btn.getAttribute('data-password-toggle'));
    if (!input) return;

    var shown = btn.querySelector('[data-icon-shown]');
    var hidden = btn.querySelector('[data-icon-hidden]');

    btn.addEventListener('click', function () {
      var reveal = input.type === 'password';
      input.type = reveal ? 'text' : 'password';
      btn.setAttribute('aria-label', reveal ? 'Hide password' : 'Show password');
      if (shown && hidden) {
        shown.classList.toggle('hidden', reveal);
        hidden.classList.toggle('hidden', !reveal);
      }
    });
  });
}

// Live "passwords don't match" validation between #password and
// #confirm_password, wherever both are present on the page.
function initConfirmPasswordMatch() {
  var pw = document.getElementById('password');
  var confirm = document.getElementById('confirm_password');
  if (!pw || !confirm) return;

  var check = function () {
    confirm.setCustomValidity(confirm.value && confirm.value !== pw.value ? 'Passwords do not match' : '');
  };
  pw.addEventListener('input', check);
  confirm.addEventListener('input', check);
}

// Fills in a 4-segment strength bar (#password-strength containing four
// [data-bar] elements and one [data-label]) as the user types into
// #password. Purely cosmetic guidance, not a hard requirement.
function initPasswordStrengthMeter() {
  var pw = document.getElementById('password');
  var meter = document.getElementById('password-strength');
  if (!pw || !meter) return;

  var bars = meter.querySelectorAll('[data-bar]');
  var label = meter.querySelector('[data-label]');
  var labels = ['Too short', 'Weak', 'Okay', 'Good', 'Strong'];
  var colors = ['bg-danger', 'bg-danger', 'bg-warn', 'bg-brand', 'bg-success'];

  pw.addEventListener('input', function () {
    var score = passwordScore(pw.value);
    bars.forEach(function (bar, i) {
      bar.className = 'h-1 flex-1 rounded-full transition-colors duration-200 ' +
        (pw.value && i < score ? colors[score] : 'bg-editor-border');
    });
    if (label) label.textContent = pw.value ? labels[score] : '';
  });
}

function passwordScore(v) {
  if (v.length < 8) return 0;
  var score = 1;
  if (v.length >= 12) score++;
  if (/[A-Z]/.test(v) && /[a-z]/.test(v)) score++;
  if (/[0-9]/.test(v)) score++;
  if (/[^A-Za-z0-9]/.test(v)) score++;
  return Math.min(score, 4);
}
