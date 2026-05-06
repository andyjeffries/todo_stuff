// Slide-over detail panel. Hidden by default; open/close via the global
// TodoStuff helper (also bound to Escape and the panel's close button).
(function () {
  const panel = document.getElementById('detail-panel');
  const backdrop = document.getElementById('detail-backdrop');
  if (!panel) return;

  function open() {
    panel.classList.remove('translate-x-full');
    panel.classList.add('translate-x-0');
    panel.setAttribute('aria-hidden', 'false');
    if (backdrop) {
      backdrop.classList.remove('opacity-0', 'pointer-events-none');
      backdrop.classList.add('opacity-100');
    }
  }

  function close() {
    panel.classList.remove('translate-x-0');
    panel.classList.add('translate-x-full');
    panel.setAttribute('aria-hidden', 'true');
    if (backdrop) {
      backdrop.classList.remove('opacity-100');
      backdrop.classList.add('opacity-0', 'pointer-events-none');
    }
  }

  panel.querySelectorAll('[data-detail-close]').forEach(function (b) {
    b.addEventListener('click', close);
  });
  if (backdrop) backdrop.addEventListener('click', close);

  document.querySelectorAll('[data-detail-open]').forEach(function (b) {
    b.addEventListener('click', open);
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && panel.getAttribute('aria-hidden') === 'false') close();
  });

  window.TodoStuff = window.TodoStuff || {};
  window.TodoStuff.openDetail = open;
  window.TodoStuff.closeDetail = close;
})();

// Toggle the Today empty-state when the list gains/loses children via HTMX.
(function () {
  function syncEmpty() {
    const list = document.getElementById('task-list');
    const empty = document.getElementById('task-empty');
    if (!list || !empty) return;
    empty.hidden = list.children.length > 0;
  }
  document.addEventListener('htmx:afterSwap', syncEmpty);
  document.addEventListener('htmx:load', syncEmpty);
})();

// Generic [data-toggle] click handler: toggles `.hidden` on the targeted
// element. Used by the sidebar "+ project" button and the project-page
// edit/delete affordances.
(function () {
  document.addEventListener('click', function (e) {
    const trigger = e.target.closest('[data-toggle]');
    if (!trigger) return;
    const target = document.querySelector(trigger.dataset.toggle);
    if (!target) return;
    target.classList.toggle('hidden');
    if (!target.classList.contains('hidden')) {
      const focusable = target.querySelector('input, textarea, select, button');
      if (focusable) focusable.focus();
    }
  });
})();

// Quick-add modal. Triggered by any [data-quick-add] button (sidebar `+` and
// bottom-right FAB). Posts via HTMX with hx-swap="none" — the user accepts a
// "fire and forget" capture; their current view doesn't auto-update.
//
// On a successful submit:
//   1. show a brief "Added" checkmark next to the input,
//   2. clear the input,
//   3. refocus it for rapid entry of more tasks.
// Esc / backdrop / clicking the FAB again all dismiss.
(function () {
  const modal = document.getElementById('quick-add');
  const backdrop = document.getElementById('quick-add-backdrop');
  const form = document.getElementById('quick-add-form');
  const input = document.getElementById('quick-add-input');
  const success = document.getElementById('quick-add-success');
  if (!modal || !backdrop || !form || !input) return;

  const VISIBLE = ['opacity-100', 'pointer-events-auto', 'scale-100'];
  const HIDDEN = ['opacity-0', 'pointer-events-none', 'scale-95'];

  function open() {
    modal.classList.remove(...HIDDEN);
    modal.classList.add(...VISIBLE);
    modal.setAttribute('aria-hidden', 'false');
    backdrop.classList.remove('opacity-0', 'pointer-events-none');
    backdrop.classList.add('opacity-100', 'pointer-events-auto');
    input.value = '';
    success.classList.remove('opacity-100');
    success.classList.add('opacity-0');
    setTimeout(function () { input.focus(); }, 50);
  }

  function close() {
    modal.classList.remove(...VISIBLE);
    modal.classList.add(...HIDDEN);
    modal.setAttribute('aria-hidden', 'true');
    backdrop.classList.remove('opacity-100', 'pointer-events-auto');
    backdrop.classList.add('opacity-0', 'pointer-events-none');
  }

  document.addEventListener('click', function (e) {
    const trigger = e.target.closest('[data-quick-add]');
    if (!trigger) return;
    e.preventDefault();
    open();
  });

  backdrop.addEventListener('click', close);
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && modal.getAttribute('aria-hidden') === 'false') close();
  });

  form.addEventListener('htmx:afterRequest', function (e) {
    if (!e.detail.successful) return;
    success.classList.remove('opacity-0');
    success.classList.add('opacity-100');
    input.value = '';
    input.focus();
    setTimeout(function () {
      success.classList.remove('opacity-100');
      success.classList.add('opacity-0');
    }, 1200);
    // Refresh the underlying view's task list so the new row appears
    // behind the still-open modal. Pages without a #task-list (e.g.
    // Logbook) silently no-op. The server-side view filter decides
    // whether the new task is actually in scope (Inbox, Today, Anytime
    // → yes; Upcoming, Project pages → no), so this is safe everywhere.
    if (document.getElementById('task-list') && typeof htmx !== 'undefined') {
      htmx.ajax('GET', window.location.pathname, {
        target: '#task-list',
        swap: 'outerHTML',
        select: '#task-list',
      });
    }
  });

  window.TodoStuff = window.TodoStuff || {};
  window.TodoStuff.openQuickAdd = open;
  window.TodoStuff.closeQuickAdd = close;
})();

// Project icon picker. Click a [data-icon-id] button → write its ID into the
// sibling hidden <input data-icon-input> and toggle the visual selected state
// across the picker's buttons. Selected styling lives in classes that mirror
// the server-rendered "selected" branch in project-icon-button.
(function () {
  const SELECTED_CLASSES = ['border-amber-400', 'bg-amber-100', 'text-amber-700'];
  const UNSELECTED_CLASSES = ['border-transparent', 'text-stone-500',
                              'hover:bg-stone-100', 'hover:text-stone-800'];

  function setSelected(btn, on) {
    if (on) {
      btn.classList.remove(...UNSELECTED_CLASSES);
      btn.classList.add(...SELECTED_CLASSES);
    } else {
      btn.classList.remove(...SELECTED_CLASSES);
      btn.classList.add(...UNSELECTED_CLASSES);
    }
  }

  document.addEventListener('click', function (e) {
    const btn = e.target.closest('[data-icon-id]');
    if (!btn) return;
    const picker = btn.closest('[data-icon-picker]');
    if (!picker) return;
    e.preventDefault();

    const form = picker.closest('form');
    const input = form && form.querySelector('[data-icon-input]');
    if (input) input.value = btn.dataset.iconId;

    picker.querySelectorAll('[data-icon-id]').forEach(function (b) {
      setSelected(b, b === btn);
    });
  });
})();

