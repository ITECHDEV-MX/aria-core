// aria-quicksearch.js — Cmd+K (Mac) / Ctrl+K (Linux/Win) quick switcher.
// Vanilla JS sin alpine. El modal está siempre presente en layout.templ;
// este script lo hace toggleable con teclado y maneja navegación arrows + Enter/Esc.
//
// Endpoint backend: /dashboard/quick-search?q=X (HTMX).

(function () {
  'use strict';

  function $(sel, root) { return (root || document).querySelector(sel); }
  function $$(sel, root) { return Array.from((root || document).querySelectorAll(sel)); }

  function isMac() {
    return /Mac|iPhone|iPad/.test(navigator.platform);
  }

  function openSwitcher() {
    var el = $('#aria-quickswitcher');
    if (!el) return;
    el.removeAttribute('hidden');
    var input = $('#aria-qs-input');
    if (input) {
      input.value = '';
      // Trigger HTMX para que limpie hint/resultados.
      if (window.htmx) { window.htmx.trigger(input, 'keyup'); }
      input.focus();
    }
  }

  function closeSwitcher() {
    var el = $('#aria-quickswitcher');
    if (!el) return;
    el.setAttribute('hidden', '');
  }

  function isOpen() {
    var el = $('#aria-quickswitcher');
    return el && !el.hasAttribute('hidden');
  }

  // ─── Keyboard handlers ────────────────────────────────────────────────
  document.addEventListener('keydown', function (e) {
    var meta = isMac() ? e.metaKey : e.ctrlKey;
    if (meta && (e.key === 'k' || e.key === 'K')) {
      e.preventDefault();
      if (isOpen()) closeSwitcher(); else openSwitcher();
      return;
    }
    if (!isOpen()) return;

    if (e.key === 'Escape') {
      e.preventDefault();
      closeSwitcher();
      return;
    }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      moveActive(e.key === 'ArrowDown' ? 1 : -1);
      return;
    }
    if (e.key === 'Enter') {
      var active = $('.aria-qs-item.active');
      if (active) {
        e.preventDefault();
        var url = active.getAttribute('data-url');
        if (url) window.location.href = url;
      }
    }
  });

  function getItems() {
    return $$('#aria-qs-results .aria-qs-item');
  }

  function moveActive(delta) {
    var items = getItems();
    if (!items.length) return;
    var idx = items.findIndex(function (i) { return i.classList.contains('active'); });
    if (idx < 0) idx = (delta > 0) ? -1 : items.length;
    var next = (idx + delta + items.length) % items.length;
    items.forEach(function (i, n) {
      if (n === next) {
        i.classList.add('active');
        if (i.scrollIntoView) i.scrollIntoView({ block: 'nearest' });
      } else {
        i.classList.remove('active');
      }
    });
  }

  // Después de cada swap HTMX en results, reset active al primer item.
  document.body.addEventListener('htmx:afterSwap', function (e) {
    if (!e.target || e.target.id !== 'aria-qs-results') return;
    var items = getItems();
    items.forEach(function (i) { i.classList.remove('active'); });
    if (items.length) items[0].classList.add('active');
  });

  // Click en overlay backdrop cierra (no clicks dentro del modal).
  document.addEventListener('click', function (e) {
    var overlay = $('#aria-quickswitcher');
    if (!overlay || overlay.hasAttribute('hidden')) return;
    var modal = $('.aria-qs-modal', overlay);
    if (!modal) return;
    if (e.target === overlay) closeSwitcher();
  });

  // Tree drag & drop via HTML5 — drag a un node y drop sobre otro
  // setea parent_id = target.id. Se posterga: por ahora click-only.
  // TODO: integrar sortable.js o pure HTML5 drag para reorder.

  // Refresh tree después de cualquier cambio (create/update/move/archive).
  document.body.addEventListener('pages:tree-changed', function () {
    var tree = $('#pages-tree');
    if (!tree) return;
    if (window.htmx) { window.htmx.trigger(tree, 'load'); }
  });
})();
