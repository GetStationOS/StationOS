// Linked workspace tabs share scroll restoration without becoming ARIA tab widgets.
(() => {
  const positions = new Map();
  const restored = new WeakSet();
  function restore() {
    document.querySelectorAll('[data-tab-scroll-key]').forEach(nav => {
      if (restored.has(nav)) return;
      restored.add(nav);
      const key = nav.dataset.tabScrollKey;
      if (positions.has(key)) nav.scrollLeft = positions.get(key);
      const active = nav.querySelector('a[aria-current="page"]');
      if (active) {
        const viewport = nav.getBoundingClientRect(), tab = active.getBoundingClientRect();
        if (tab.left < viewport.left) nav.scrollLeft -= viewport.left - tab.left;
        else if (tab.right > viewport.right) nav.scrollLeft += tab.right - viewport.right;
      }
      positions.set(key, nav.scrollLeft);
    });
  }
  document.addEventListener('scroll', event => {
    const nav = event.target;
    if (nav.matches?.('[data-tab-scroll-key]') && nav.isConnected) positions.set(nav.dataset.tabScrollKey, nav.scrollLeft);
  }, {capture:true, passive:true});
  document.addEventListener('htmx:beforeSwap', event => {
    if (event.detail.target?.id !== 'main' || !event.detail.shouldSwap) return;
    document.querySelectorAll('[data-tab-scroll-key]').forEach(nav => positions.set(nav.dataset.tabScrollKey, nav.scrollLeft));
  });
  document.addEventListener('DOMContentLoaded', restore);
  document.addEventListener('htmx:afterSwap', restore);
  document.addEventListener('htmx:historyRestore', restore);
  window.addEventListener('pageshow', restore);
})();
