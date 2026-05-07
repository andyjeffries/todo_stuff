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
    if (e.key !== 'Escape') return;
    // Don't dismiss the panel if a child overlay is consuming the Escape —
    // e.g. the project-select dropdown should close first.
    if (document.querySelector('[data-listbox-list]:not(.hidden)')) return;
    if (panel.getAttribute('aria-hidden') === 'false') close();
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

// Refresh the active task list when the server signals a side-effect change.
// The complete-task handler emits HX-Trigger: tasks-list-changed when a
// recurring task's completion regenerated a new instance — the new instance
// may belong to the current view (e.g. daily recurrence on /today), so we
// re-fetch and let the server's filter decide. Pages without a #task-list
// silently no-op.
(function () {
  document.body.addEventListener('tasks-list-changed', function () {
    if (!document.getElementById('task-list') || typeof htmx === 'undefined') return;
    htmx.ajax('GET', window.location.pathname, {
      target: '#task-list',
      swap: 'outerHTML',
      select: '#task-list',
    });
  });
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

// Mobile sidebar drawer. Off-canvas on <sm; in-flow on ≥sm (Tailwind handles
// the layout switch via `sm:relative sm:translate-x-0`). This module just
// adds/removes `translate-x-0` to slide it in, and toggles the backdrop.
(function () {
  const sidebar = document.getElementById('sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');
  if (!sidebar) return;

  function open() {
    sidebar.classList.remove('-translate-x-full');
    sidebar.classList.add('translate-x-0');
    sidebar.setAttribute('aria-hidden', 'false');
    if (backdrop) {
      backdrop.classList.remove('opacity-0', 'pointer-events-none');
      backdrop.classList.add('opacity-100');
    }
  }
  function close() {
    sidebar.classList.add('-translate-x-full');
    sidebar.classList.remove('translate-x-0');
    sidebar.setAttribute('aria-hidden', 'true');
    if (backdrop) {
      backdrop.classList.remove('opacity-100');
      backdrop.classList.add('opacity-0', 'pointer-events-none');
    }
  }

  document.addEventListener('click', function (e) {
    if (e.target.closest('[data-sidebar-open]')) { open(); return; }
    if (e.target.closest('[data-sidebar-close]')) { close(); return; }
  });
  if (backdrop) backdrop.addEventListener('click', close);

  // Tapping a nav link should auto-close the drawer so the user lands in the
  // chosen view without an extra dismiss step. Only matters on mobile; on
  // desktop the sidebar is always visible so the toggle is a no-op.
  sidebar.querySelectorAll('a[href]').forEach(function (a) {
    a.addEventListener('click', close);
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && sidebar.getAttribute('aria-hidden') === 'false') close();
  });
})();

// Pushover key controls on /profile. The masked at-rest UI hides the saved
// key behind bullets; Change reveals an editable text input pre-filled with
// the saved value (so the user can tweak/correct rather than retype), and
// Remove clears the value and submits the form so the server-side delete
// path runs.
(function () {
  document.addEventListener('click', function (e) {
    const change = e.target.closest('[data-pushover-change]');
    if (change) {
      const saved = document.querySelector('[data-pushover-saved]');
      const input = document.querySelector('[data-pushover-input]');
      const hint = document.querySelector('[data-pushover-hint]');
      if (saved && input) {
        saved.classList.add('hidden');
        input.type = 'text';
        input.focus();
        input.select();
        if (hint) hint.classList.remove('hidden');
      }
      return;
    }
    const remove = e.target.closest('[data-pushover-remove]');
    if (remove) {
      const input = document.querySelector('[data-pushover-input]');
      if (input) {
        input.value = '';
        const form = input.closest('form');
        if (form) form.submit();
      }
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

// Due-date quick chips in the task detail panel. Click "Today" / "Tomorrow" /
// "Next week" → set the date input; "Clear" → empty both date and time. After
// updating the inputs, dispatch a `change` event so the form's existing
// hx-trigger="change ... from:input" picks it up and PUTs.
(function () {
  function ymd(d) {
    const y = d.getFullYear();
    const m = String(d.getMonth() + 1).padStart(2, '0');
    const day = String(d.getDate()).padStart(2, '0');
    return `${y}-${m}-${day}`;
  }

  document.addEventListener('click', function (e) {
    const btn = e.target.closest('[data-due-set]');
    if (!btn) return;
    e.preventDefault();
    const fields = btn.closest('[data-due-fields]');
    if (!fields) return;
    const dateInput = fields.querySelector('input[name="due_date"]');
    const timeInput = fields.querySelector('input[name="due_time"]');
    if (!dateInput) return;

    const today = new Date();
    today.setHours(0, 0, 0, 0);

    switch (btn.dataset.dueSet) {
      case 'today':
        dateInput.value = ymd(today);
        break;
      case 'tomorrow': {
        const d = new Date(today); d.setDate(d.getDate() + 1);
        dateInput.value = ymd(d);
        break;
      }
      case 'next-week': {
        const d = new Date(today); d.setDate(d.getDate() + 7);
        dateInput.value = ymd(d);
        break;
      }
      case 'clear':
        dateInput.value = '';
        if (timeInput) timeInput.value = '';
        break;
    }
    dateInput.dispatchEvent(new Event('change', { bubbles: true }));
  });
})();

// Reminder section visibility. The Reminder dropdown is only meaningful
// when the task has both a due date AND a due time (the offset anchors
// against that datetime). Toggle visibility as the date/time inputs change
// so users don't see a dead control. The server-side Update clears the
// reminder columns automatically when due_date or due_time is cleared, so
// we only need to manage UI here — no need to reset the listbox value.
(function () {
  function syncReminderVisibility(form) {
    const fields = form.querySelector('[data-reminder-fields]');
    if (!fields) return;
    const dateEl = form.querySelector('input[name="due_date"]');
    const timeEl = form.querySelector('input[name="due_time"]');
    const canRemind = !!(dateEl && dateEl.value && timeEl && timeEl.value);
    fields.classList.toggle('hidden', !canRemind);
  }

  document.addEventListener('change', function (e) {
    const input = e.target;
    if (!input.matches('input[name="due_date"], input[name="due_time"]')) return;
    const form = input.closest('form');
    if (form) syncReminderVisibility(form);
  });
})();

// Recurrence frequency change → toggle visibility of the regeneration-type
// radio group. When frequency is "" (Never) the type radios are meaningless
// and just add visual noise; the form still submits all three fields and
// the handler treats frequency="" as "remove rule".
(function () {
  document.addEventListener('change', function (e) {
    const select = e.target;
    if (!select.matches('select[name="recurrence_frequency"]')) return;
    const fields = select.closest('[data-recurrence-fields]');
    if (!fields) return;
    const group = fields.querySelector('[data-recurrence-type-group]');
    if (!group) return;
    group.classList.toggle('hidden', select.value === '');
  });
})();

// Click-to-edit notes in the task detail panel. The detail panel renders the
// notes preview by default; clicking it hides the preview and reveals a
// textarea with the markdown source. Blurring the textarea swaps back. The
// form's existing change-debounced HTMX PUT picks up edits, and the server's
// OOB response replaces the preview's HTML so it stays in sync.
(function () {
  document.addEventListener('click', function (e) {
    // Let links inside rendered notes navigate normally.
    if (e.target.closest('a')) return;
    const display = e.target.closest('[data-notes-display]');
    if (!display) return;
    const block = display.closest('[data-notes-block]');
    const textarea = block && block.querySelector('[data-notes-edit]');
    if (!textarea) return;
    display.classList.add('hidden');
    textarea.classList.remove('hidden');
    textarea.focus();
    const len = textarea.value.length;
    try { textarea.setSelectionRange(len, len); } catch (_) {}
  });

  document.addEventListener('focusout', function (e) {
    const textarea = e.target.closest('[data-notes-edit]');
    if (!textarea) return;
    const block = textarea.closest('[data-notes-block]');
    const display = block && block.querySelector('[data-notes-display]');
    if (!display) return;
    textarea.classList.add('hidden');
    display.classList.remove('hidden');
  });
})();

// Generic custom listbox. Markup contract (any element with these attrs):
//
//   [data-listbox]            — root wrapper
//     [data-listbox-input]    — hidden <input> carrying the form value
//     [data-listbox-toggle]   — button that opens/closes the listbox
//       [data-listbox-display] — span inside the toggle (shows current pick)
//     [data-listbox-list]     — the listbox <ul> (hidden by default)
//       [data-listbox-option] — each clickable <button> with data-value
//
// Selecting an option mirrors its innerHTML into the display span and writes
// the value into the hidden input. If the value changed we dispatch `change`
// on the hidden input so a form's HTMX trigger fires.
//
// Used by task-project-select and task-reminder-select.
(function () {
  function closeAll(except) {
    document.querySelectorAll('[data-listbox]').forEach(function (el) {
      if (el === except) return;
      const list = el.querySelector('[data-listbox-list]');
      const toggle = el.querySelector('[data-listbox-toggle]');
      if (list) list.classList.add('hidden');
      if (toggle) toggle.setAttribute('aria-expanded', 'false');
    });
  }

  document.addEventListener('click', function (e) {
    const toggle = e.target.closest('[data-listbox-toggle]');
    if (toggle) {
      e.preventDefault();
      const wrap = toggle.closest('[data-listbox]');
      if (!wrap) return;
      const list = wrap.querySelector('[data-listbox-list]');
      if (!list) return;
      const willOpen = list.classList.contains('hidden');
      closeAll(willOpen ? wrap : null);
      list.classList.toggle('hidden', !willOpen);
      toggle.setAttribute('aria-expanded', willOpen ? 'true' : 'false');
      return;
    }

    const opt = e.target.closest('[data-listbox-option]');
    if (opt) {
      e.preventDefault();
      const wrap = opt.closest('[data-listbox]');
      if (!wrap) return;
      const input = wrap.querySelector('[data-listbox-input]');
      const display = wrap.querySelector('[data-listbox-display]');
      if (!input || !display) return;
      const newValue = opt.dataset.value || '';
      const old = input.value;
      input.value = newValue;
      // Mirror the picked option's content into the toggle button so the
      // selection's icon + label show up immediately, no round-trip needed.
      display.innerHTML = opt.innerHTML;
      closeAll(null);
      if (newValue !== old) {
        input.dispatchEvent(new Event('change', { bubbles: true }));
      }
      return;
    }

    // Click outside any open listbox → close them.
    if (!e.target.closest('[data-listbox]')) closeAll(null);
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') closeAll(null);
  });
})();

// Browser notifications for due reminders.
//
//   1. The sidebar exposes an [data-notif-toggle] button that drives the
//      permission state. Browsers require Notification.requestPermission()
//      to come from a real user gesture (page-load calls are silently
//      ignored), so the button click is the gesture.
//   2. Poll /api/reminders/due every 60s. The endpoint atomically marks
//      everything it returns as delivered, so we never re-fire the same row.
//   3. For each row, post a `new Notification`. Clicking the notification
//      focuses the tab and opens the task detail panel.
//
// Pages without the [data-app-shell] marker (login, setup) are skipped.
(function () {
  if (!document.querySelector('[data-app-shell]')) return;
  const supported = ('Notification' in window);

  // -------- Sidebar enable-reminders button + blocked-instructions modal --
  const blockedModal = document.getElementById('notif-blocked');
  const blockedBackdrop = document.getElementById('notif-blocked-backdrop');
  const BLOCKED_VISIBLE = ['opacity-100', 'pointer-events-auto', 'scale-100'];
  const BLOCKED_HIDDEN  = ['opacity-0', 'pointer-events-none', 'scale-95'];

  function openBlockedModal() {
    if (!blockedModal || !blockedBackdrop) return;
    blockedModal.classList.remove(...BLOCKED_HIDDEN);
    blockedModal.classList.add(...BLOCKED_VISIBLE);
    blockedModal.setAttribute('aria-hidden', 'false');
    blockedBackdrop.classList.remove('opacity-0', 'pointer-events-none');
    blockedBackdrop.classList.add('opacity-100', 'pointer-events-auto');
  }
  function closeBlockedModal() {
    if (!blockedModal || !blockedBackdrop) return;
    blockedModal.classList.remove(...BLOCKED_VISIBLE);
    blockedModal.classList.add(...BLOCKED_HIDDEN);
    blockedModal.setAttribute('aria-hidden', 'true');
    blockedBackdrop.classList.remove('opacity-100', 'pointer-events-auto');
    blockedBackdrop.classList.add('opacity-0', 'pointer-events-none');
  }
  if (blockedBackdrop) blockedBackdrop.addEventListener('click', closeBlockedModal);
  document.addEventListener('click', function (e) {
    if (e.target.closest('[data-notif-blocked-close]')) closeBlockedModal();
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && blockedModal && blockedModal.getAttribute('aria-hidden') === 'false') {
      closeBlockedModal();
    }
  });

  function syncNotifButton() {
    const btn = document.querySelector('[data-notif-toggle]');
    if (!btn) return;
    const label = btn.querySelector('[data-notif-label]');
    const iconOn = btn.querySelector('[data-notif-icon="on"]');
    const iconOff = btn.querySelector('[data-notif-icon="off"]');
    if (!supported) {
      btn.classList.add('hidden');
      return;
    }
    const state = Notification.permission;
    btn.dataset.notifState = state;
    if (state === 'granted') {
      // Once granted, the button is just a status indicator; hide it to
      // keep the sidebar tidy.
      btn.classList.add('hidden');
      return;
    }
    btn.classList.remove('hidden');
    if (state === 'denied') {
      if (label) label.textContent = 'Reminders blocked';
      if (iconOn) iconOn.classList.add('hidden');
      if (iconOff) iconOff.classList.remove('hidden');
      btn.title = 'Click to see how to re-enable notifications for this site.';
    } else { // default
      if (label) label.textContent = 'Enable reminders';
      if (iconOn) iconOn.classList.remove('hidden');
      if (iconOff) iconOff.classList.add('hidden');
      btn.title = 'Click to allow browser notifications for due reminders.';
    }
  }

  document.addEventListener('click', function (e) {
    const btn = e.target.closest('[data-notif-toggle]');
    if (!btn) return;
    e.preventDefault();
    if (!supported) return;
    const state = Notification.permission;
    if (state === 'denied') {
      openBlockedModal();
      return;
    }
    if (state !== 'default') return;
    try {
      const r = Notification.requestPermission();
      if (r && typeof r.then === 'function') {
        r.then(function (next) {
          syncNotifButton();
          if (next === 'denied') openBlockedModal();
        }).catch(function () { syncNotifButton(); });
      } else {
        // Older callback-style API — re-sync on next tick.
        setTimeout(syncNotifButton, 0);
      }
    } catch (_) { syncNotifButton(); }
  });

  syncNotifButton();
  // Re-sync after HTMX swaps in case the sidebar was replaced.
  document.body.addEventListener('htmx:afterSwap', syncNotifButton);

  if (!supported) return;

  const POLL_MS = 60 * 1000;

  async function poll() {
    try {
      const res = await fetch('/api/reminders/due', {
        credentials: 'same-origin',
        headers: { Accept: 'application/json' },
      });
      if (!res.ok) return;
      const reminders = await res.json();
      if (!Array.isArray(reminders) || reminders.length === 0) return;
      if (Notification.permission !== 'granted') return;
      reminders.forEach(function (rem) {
        try {
          const n = new Notification(rem.title || 'Reminder', {
            body: rem.notes || '',
            tag: 'todostuff-reminder-' + rem.id,
          });
          n.onclick = function () {
            window.focus();
            // Bring the user to /today and pop the task detail. We don't
            // know which view contains the task; /today is a safe default
            // and always exists for an authenticated user.
            if (rem.id && typeof htmx !== 'undefined') {
              htmx.ajax('GET', '/tasks/' + rem.id, {
                target: '#detail-panel-body',
                swap: 'innerHTML',
              }).then(function () {
                if (window.TodoStuff && window.TodoStuff.openDetail) {
                  window.TodoStuff.openDetail();
                }
              });
            }
            n.close();
          };
        } catch (_) { /* notification construction can throw on iOS PWA, ignore */ }
      });
    } catch (_) { /* network error — try again next tick */ }
  }

  // Fire one immediate poll so reminders queued while the tab was closed
  // surface as soon as the user returns; then settle into the interval.
  poll();
  setInterval(poll, POLL_MS);
})();

// Drag-and-drop for task lists, the sidebar projects list, and cross-list
// "drop a task on a project to assign it". Uses SortableJS (vendored).
//
// Three Sortables coexist:
//   #task-list        — within-list reorder + draggable out into project rows
//   #project-list     — within-list reorder of projects only (separate group)
//   [data-project-drop] — per-project-row drop zones; receive task drops and
//                         fire the assign API. Nested inside #project-list.
//
// `handle: '[data-drag-handle]'` on the reorder lists keeps row clicks
// (checkbox, title) intact. A failed reorder leaves the list visually
// correct; we just lose the persisted order — refreshing snaps back.
(function () {
  if (typeof Sortable === 'undefined') return;

  // Class option values must be SINGLE class names — SortableJS feeds them to
  // classList.add(), which throws SyntaxError on space-separated strings and
  // silently aborts the drag. Style the look via that class in app.css.
  const COMMON = {
    handle: '[data-drag-handle]',
    animation: 150,
    // delayOnTouchOnly + delay together: long-press to drag on touch devices,
    // instant on desktop. Without the touch delay, scrolling a list on mobile
    // can accidentally trigger a drag.
    delayOnTouchOnly: true,
    delay: 150,
    ghostClass: 'sortable-ghost',
    chosenClass: 'sortable-chosen',
    dragClass: 'sortable-drag',
  };

  function postOrder(url, ids) {
    if (!ids || ids.length < 2) return;
    const body = new URLSearchParams();
    ids.forEach(function (id) { body.append('ids', id); });
    fetch(url, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString(),
    }).catch(function () { /* surface nothing — user already sees the new order */ });
  }

  // assignTaskToProject moves a task into a project. We use a plain fetch
  // for the PUT (rather than htmx.ajax) so we can sequence the follow-up
  // refreshes without depending on htmx's promise semantics. After the PUT
  // we explicitly refresh:
  //   1. the source task list — the task may no longer belong to the
  //      current view (e.g. dragged out of Inbox into a project), and a
  //      full GET of the page's #task-list is the simplest way to let the
  //      server's view filter decide what stays.
  //   2. the detail panel — if it's open against this same task, re-fetch
  //      so the Project selector + any project-tagged fields update.
  function assignTaskToProject(taskId, projectId) {
    const body = new URLSearchParams({ project_id: projectId });
    fetch('/tasks/' + taskId, {
      method: 'PUT',
      credentials: 'same-origin',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
        'HX-Request': 'true',
        'HX-Current-Url': window.location.href,
      },
      body: body.toString(),
    }).then(function () {
      if (typeof htmx === 'undefined') return;
      // Refresh the active task list so the source row reflects the new
      // assignment (or disappears if it no longer belongs to this view).
      if (document.getElementById('task-list')) {
        htmx.ajax('GET', window.location.pathname, {
          target: '#task-list',
          swap: 'outerHTML',
          select: '#task-list',
        });
      }
      // Refresh the detail panel if it's currently showing this task.
      const form = document.getElementById('task-detail-form');
      if (form && form.getAttribute('hx-put') === '/tasks/' + taskId) {
        htmx.ajax('GET', '/tasks/' + taskId, {
          target: '#detail-panel-body',
          swap: 'innerHTML',
        });
      }
    }).catch(function () { /* network/server error — leave UI as-is */ });
  }

  function initTaskList() {
    // Bind Sortable to each [data-task-list] <ul>. Most views have one (the
    // flat task <ul>); Upcoming has one per day group. Binding to the per-list
    // <ul> instead of a wrapper <div> keeps draggable items at the row level
    // — otherwise SortableJS treats whatever direct children the bound element
    // has as draggable, and on Upcoming that's the per-day <section> blocks
    // (header + list together), which is not what anyone wants.
    document.querySelectorAll('[data-task-list]').forEach(function (list) {
      if (list.dataset.sortableInit === '1') return;
      list.dataset.sortableInit = '1';
      Sortable.create(list, Object.assign({}, COMMON, {
        // pull: 'clone' so the original <li> stays put while the clone goes
        // into the project drop zone (whose onAdd fires the assign API).
        // put: false rejects drops from other task lists — relevant on
        // Upcoming, where dropping a task into a different day's list would
        // otherwise visually succeed but snap back on refresh (the date
        // didn't change). Project drop zones have pull:false so they never
        // push back here; nothing legitimate is lost.
        group: { name: 'tasks-and-projects', pull: 'clone', put: false },
        onEnd: function (evt) {
          // Cross-list move: destination's onAdd handles persistence. Skip.
          if (evt.from !== evt.to) return;
          const ids = Array.from(list.querySelectorAll('li[id^="task-"]'))
            .map(function (li) { return li.id.replace(/^task-/, ''); });
          postOrder('/tasks/reorder', ids);
        },
      }));
    });
  }

  function initProjectList() {
    const list = document.getElementById('project-list');
    if (!list || list.dataset.sortableInit === '1') return;
    list.dataset.sortableInit = '1';
    Sortable.create(list, Object.assign({}, COMMON, {
      // Separate group — project rows reorder among themselves only and
      // never accept task drops at this level (nested drop zones do that).
      group: 'projects',
      // Only treat direct-child project rows as draggable items; the nested
      // drop-zone Sortable's items inside each row should be ignored here.
      draggable: '[data-project-id]',
      onEnd: function () {
        const ids = Array.from(list.querySelectorAll('[data-project-id]'))
          .map(function (el) { return el.dataset.projectId; });
        postOrder('/projects/reorder', ids);
      },
    }));
  }

  function initProjectDropZones() {
    document.querySelectorAll('[data-project-drop]').forEach(function (zone) {
      if (zone.dataset.sortableInit === '1') return;
      zone.dataset.sortableInit = '1';
      Sortable.create(zone, {
        group: { name: 'tasks-and-projects', pull: false, put: true },
        // sort:false + draggable selector that matches nothing here keeps the
        // row's own children (drag handle, link) immobile. The zone only
        // exists to receive task drops.
        sort: false,
        draggable: '.never-match',
        animation: 0,
        onAdd: function (evt) {
          const taskId = (evt.item.id || '').replace(/^task-/, '');
          const projectId = zone.dataset.projectId;
          // SortableJS just inserted the dragged li into this row. Yank it —
          // the assign response will OOB-delete the original row from the
          // task list when the task no longer belongs to the current view.
          if (evt.item && evt.item.parentNode) evt.item.parentNode.removeChild(evt.item);
          if (taskId && projectId) assignTaskToProject(taskId, projectId);
        },
      });
    });
  }

  function initAll() {
    initTaskList();
    initProjectList();
    initProjectDropZones();
  }

  initAll();
  // HTMX swaps replace #task-list and #project-list with fresh DOM, so we
  // re-bind after every swap. Sortable.create on the same node re-initialises
  // cleanly; the dataset guard prevents double-init for unrelated swaps.
  document.body.addEventListener('htmx:afterSwap', function () {
    document.querySelectorAll('[data-task-list]').forEach(function (l) {
      delete l.dataset.sortableInit;
    });
    const p = document.getElementById('project-list');
    if (p) delete p.dataset.sortableInit;
    document.querySelectorAll('[data-project-drop]').forEach(function (z) {
      delete z.dataset.sortableInit;
    });
    initAll();
  });
})();

// Keyboard shortcuts:
//   n      — open quick-add modal (skip when typing in an input)
//   Esc    — handled per-overlay above (detail, sidebar, modal, listbox)
//   Enter  — native form submit handles "save" in the detail panel and
//            quick-add; we don't override it.
(function () {
  function isTyping(target) {
    if (!target) return false;
    const tag = (target.tagName || '').toLowerCase();
    if (tag === 'input' || tag === 'textarea' || tag === 'select') return true;
    if (target.isContentEditable) return true;
    return false;
  }

  document.addEventListener('keydown', function (e) {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (isTyping(e.target)) return;
    if (e.key !== 'n' && e.key !== 'N') return;
    if (!window.TodoStuff || !window.TodoStuff.openQuickAdd) return;
    e.preventDefault();
    window.TodoStuff.openQuickAdd();
  });
})();

// Top-of-page progress strip for HTMX requests. Only flips on for requests
// that take >250ms — sub-perceptual local round-trips don't get any visual
// noise. Driven by htmx:beforeRequest / htmx:afterRequest events. Multiple
// concurrent requests are tracked via a counter so the bar stays up until
// the last one settles.
(function () {
  const bar = document.getElementById('htmx-progress');
  if (!bar || typeof document === 'undefined') return;

  let inflight = 0;
  let timer = null;

  function show() { bar.classList.add('active'); }
  function hide() { bar.classList.remove('active'); }

  document.body.addEventListener('htmx:beforeRequest', function () {
    inflight++;
    if (timer) return;
    timer = setTimeout(function () {
      timer = null;
      if (inflight > 0) show();
    }, 250);
  });

  function settle() {
    inflight = Math.max(0, inflight - 1);
    if (inflight > 0) return;
    if (timer) { clearTimeout(timer); timer = null; }
    hide();
  }
  document.body.addEventListener('htmx:afterRequest', settle);
  // Network failures / timeouts fire htmx:sendError or htmx:responseError but
  // not always afterRequest depending on phase, so reset on those too.
  document.body.addEventListener('htmx:sendError',     settle);
  document.body.addEventListener('htmx:responseError', settle);
})();

// Nav shortcuts: t/i/u/a/l navigate the smart lists, 1-9 jump to the nth
// project, ? opens the cheatsheet. Same skip rules as the `n` handler —
// modifiers, typing fields, and any open overlay (detail panel /
// quick-add modal / shortcuts modal) all suppress.
//
// We click the matching sidebar link rather than setting window.location so
// any link-bound handlers still fire (notably the mobile-drawer auto-close).
(function () {
  const NAV = { t: '/today', i: '/inbox', u: '/upcoming', a: '/anytime', l: '/logbook' };

  const cheatsheet = document.getElementById('shortcuts-modal');
  const cheatsheetBackdrop = document.getElementById('shortcuts-backdrop');
  const SHEET_VISIBLE = ['opacity-100', 'pointer-events-auto', 'scale-100'];
  const SHEET_HIDDEN  = ['opacity-0', 'pointer-events-none', 'scale-95'];

  function openCheatsheet() {
    if (!cheatsheet || !cheatsheetBackdrop) return;
    cheatsheet.classList.remove(...SHEET_HIDDEN);
    cheatsheet.classList.add(...SHEET_VISIBLE);
    cheatsheet.setAttribute('aria-hidden', 'false');
    cheatsheetBackdrop.classList.remove('opacity-0', 'pointer-events-none');
    cheatsheetBackdrop.classList.add('opacity-100', 'pointer-events-auto');
  }
  function closeCheatsheet() {
    if (!cheatsheet || !cheatsheetBackdrop) return;
    cheatsheet.classList.remove(...SHEET_VISIBLE);
    cheatsheet.classList.add(...SHEET_HIDDEN);
    cheatsheet.setAttribute('aria-hidden', 'true');
    cheatsheetBackdrop.classList.remove('opacity-100', 'pointer-events-auto');
    cheatsheetBackdrop.classList.add('opacity-0', 'pointer-events-none');
  }
  if (cheatsheetBackdrop) cheatsheetBackdrop.addEventListener('click', closeCheatsheet);
  document.addEventListener('click', function (e) {
    if (e.target.closest('[data-shortcuts-close]')) closeCheatsheet();
    if (e.target.closest('[data-shortcuts-open]'))  openCheatsheet();
  });

  function isTyping(target) {
    if (!target) return false;
    const tag = (target.tagName || '').toLowerCase();
    if (tag === 'input' || tag === 'textarea' || tag === 'select') return true;
    return target.isContentEditable === true;
  }

  function cheatsheetOpen() {
    return cheatsheet && cheatsheet.getAttribute('aria-hidden') === 'false';
  }

  function overlayOpen() {
    const detail = document.getElementById('detail-panel');
    if (detail && detail.getAttribute('aria-hidden') === 'false') return true;
    const qa = document.getElementById('quick-add');
    if (qa && qa.getAttribute('aria-hidden') === 'false') return true;
    if (cheatsheetOpen()) return true;
    return false;
  }

  document.addEventListener('keydown', function (e) {
    // ? closes itself if open, otherwise opens. Trigger on the literal '?'
    // (already shift-aware on most layouts). Skip while typing so it doesn't
    // hijack a real question mark in the middle of editing.
    if (e.key === '?') {
      if (isTyping(e.target)) return;
      // Allow toggle-from-open without the overlayOpen guard below skipping.
      if (cheatsheetOpen()) {
        e.preventDefault();
        closeCheatsheet();
        return;
      }
      // The overlayOpen() check below would suppress, but we want ? to be
      // available everywhere except when typing — keep it simple.
      const detail = document.getElementById('detail-panel');
      const qa = document.getElementById('quick-add');
      if (detail && detail.getAttribute('aria-hidden') === 'false') return;
      if (qa && qa.getAttribute('aria-hidden') === 'false') return;
      e.preventDefault();
      openCheatsheet();
      return;
    }

    if (e.key === 'Escape' && cheatsheetOpen()) {
      closeCheatsheet();
      return;
    }

    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (isTyping(e.target)) return;
    if (overlayOpen()) return;

    const k = e.key.toLowerCase();
    if (Object.prototype.hasOwnProperty.call(NAV, k)) {
      const link = document.querySelector(`#sidebar a[href="${NAV[k]}"]`);
      if (link) {
        e.preventDefault();
        link.click();
      }
      return;
    }
    if (/^[1-9]$/.test(e.key)) {
      const links = document.querySelectorAll('#project-list a[href^="/projects/"]');
      const idx = parseInt(e.key, 10) - 1;
      if (links[idx]) {
        e.preventDefault();
        links[idx].click();
      }
    }
  });
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

