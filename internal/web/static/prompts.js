(() => {
 let initial = '', pending = null;
 const form = () => document.querySelector('[data-prompt-form]');
 const dirty = () => { const f = form(); return f && f.elements.prompt.value !== initial; };
 function initialise() { const f = form(); if (f) { if (f.dataset.promptError !== 'true') initial = f.elements.prompt.value; pending = null; f.querySelector('[data-prompt-unsaved]').textContent = dirty() ? 'Unsaved changes' : ''; } }
 document.addEventListener('DOMContentLoaded', initialise);
 document.body.addEventListener('htmx:afterSwap', e => { if (e.detail.target?.id === 'main') initialise(); });
 document.body.addEventListener('htmx:beforeSwap', e => {
  if (e.detail.requestConfig?.elt?.closest('[data-prompt-form]') && [422,500].includes(e.detail.xhr.status) && e.detail.xhr.responseText.includes('data-prompt-form')) { e.detail.shouldSwap = true; e.detail.isError = false; }
 });
 document.body.addEventListener('input', e => { if(e.target.matches('[data-prompt-form] textarea')) document.querySelector('[data-prompt-unsaved]').textContent = dirty() ? 'Unsaved changes' : ''; });
 document.body.addEventListener('htmx:confirm', e => {
  const trigger = e.detail.elt;
  if (!dirty() || !trigger.closest('a[href]')) return;
  e.preventDefault(); pending = () => e.detail.issueRequest(true); document.querySelector('[data-prompt-discard]').showModal();
 });
 document.body.addEventListener('click', e => {
  if (e.target.closest('[data-prompt-keep]')) document.querySelector('[data-prompt-discard]').close();
  if (e.target.closest('[data-prompt-leave]')) { const next=pending; pending=null; initial=form().elements.prompt.value; document.querySelector('[data-prompt-discard]').close(); next?.(); }
 });
 window.addEventListener('beforeunload', e => { if(dirty()) {e.preventDefault();e.returnValue='';} });
})();
