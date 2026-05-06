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
