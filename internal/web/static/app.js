let stationEvents=null;
let stationEventOwner=null;
htmx.config.allowEval = false;
htmx.config.includeIndicatorStyles = false;
htmx.config.historyCacheSize = 0;
htmx.config.historyRestoreAsHxRequest = false;

function selectionState(form) {
  return JSON.stringify(Array.from(form.querySelectorAll('input[type="checkbox"]:checked')).map(input => input.name + ':' + input.value).sort());
}
function refreshChoices(form) {
  form.querySelectorAll('[data-choice-group]').forEach(group => {
    const checked = group.querySelectorAll('input[type="checkbox"]:checked').length;
    group.querySelector('[data-choice-count]').textContent = checked + ' selected';
  });
  const dirty = form.dataset.initial !== selectionState(form);
  form.dataset.dirty = String(dirty);
  form.querySelector('[data-unsaved]').textContent = dirty ? 'Unsaved changes' : '';
  form.querySelector('.selection-save').classList.toggle('dirty', dirty);
  form.querySelector('button[type="submit"]').disabled = !dirty;
}
function refreshTracks(form) {
  const boxes = Array.from(form.querySelectorAll('input[name="item"]'));
  const count = boxes.filter(input => input.checked).length;
  const all = form.querySelector('[data-select-page]');
  if (all) { all.checked = count > 0 && count === boxes.length; all.indeterminate = count > 0 && count < boxes.length; }
  const output = form.querySelector('[data-track-count]');
  if (output) output.textContent = count ? count + ' selected on this page' : 'Select tracks below';
  const button = form.querySelector('button');
  if (button) button.disabled = count === 0 || !form.querySelector('select[name="playlist"]').value;
}
function initialise() {
  syncStationEvents();
  const path = document.querySelector(".screen")?.dataset.page || location.pathname;
  const active = path.startsWith('/djs') ? 'djs' : path.startsWith('/sources') ? 'sources' : path === '/content' && !document.querySelector('.breadcrumbs') ? 'sources' : 'stations';
  document.querySelectorAll('[data-nav]').forEach(link => {
    if (link.dataset.nav === active) link.setAttribute('aria-current', 'page');
    else link.removeAttribute('aria-current');
  });
  document.querySelectorAll('[data-selection-form]').forEach(form => { form.dataset.initial = selectionState(form); refreshChoices(form); });
  document.querySelectorAll('[data-track-form]').forEach(refreshTracks);
  document.querySelectorAll('[data-dj-model]').forEach(refreshDJModel);
  document.querySelectorAll('.calendar-scroll:not([data-positioned])').forEach(calendar => {
    const cards = Array.from(calendar.querySelectorAll('.calendar-show'));
    if (cards.length) calendar.scrollTop = Math.max(0, Math.min(...cards.map(card => card.offsetTop)) - 106);
    calendar.dataset.positioned = 'true';
  });
}
function refreshDJModel(select) {
 const field = select.closest('form').querySelector('[data-custom-model]');
 field.hidden = select.value !== 'custom';
 field.querySelector('input').required = select.value === 'custom';
}
function unsavedSelections() { return !!document.querySelector('[data-selection-form][data-dirty="true"]'); }
let pendingNavigation = null;
document.body.addEventListener('htmx:confirm', event => {
  const trigger = event.detail.elt;
  const form = trigger.closest('form');
  const navigation = trigger.closest('a[href]') || (form && !form.matches('[data-selection-form]'));
  if (navigation && unsavedSelections()) {
    event.preventDefault();
    pendingNavigation = () => event.detail.issueRequest(true);
    document.getElementById('unsaved-dialog').showModal();
  }
});
window.addEventListener('beforeunload', event => { if (unsavedSelections()) { event.preventDefault(); event.returnValue = ''; } });
document.body.addEventListener('htmx:afterSwap', event => {
  const feedback = document.getElementById('feedback'); feedback.hidden = true;
  if (event.detail.target.id === 'dialog-body') {
    document.querySelector('#confirm-dialog [data-modal-error]').hidden = true;
    document.getElementById('confirm-dialog').showModal();
    document.querySelector('#confirm-dialog [data-close-dialog]')?.focus();
    return;
  }
  if (event.detail.target.id === 'main') document.querySelectorAll('dialog[open]').forEach(dialog => dialog.close());
  initialise();
  if (event.detail.target.id === 'main') {
    document.getElementById('main').focus({preventScroll:true});
  }
});
document.body.addEventListener('htmx:historyRestore', initialise);
document.body.addEventListener('htmx:beforeRequest', event => { event.detail.target.setAttribute('aria-busy', 'true'); });
document.body.addEventListener('htmx:afterRequest', event => { event.detail.target.removeAttribute('aria-busy'); });
function showError(message) {
  const feedback = document.querySelector('dialog[open] [data-modal-error]') || document.getElementById('feedback');
  feedback.textContent = message; feedback.hidden = false; feedback.focus({preventScroll:false});
}
document.body.addEventListener('htmx:responseError', event => {
  showError(event.detail.xhr.status === 401 ? 'Your session needs authentication. Reload to sign in.' : event.detail.xhr.responseText);
});
document.body.addEventListener('htmx:sendError', () => showError('Cannot reach StationOS. Your changes have not been saved. Check the connection and try again.'));
document.body.addEventListener('change', event => {
  if(event.target.matches('[data-dj-model]')) refreshDJModel(event.target);
  const form = event.target.closest('form');
  if (!form) return;
  if (form.matches('[data-selection-form]')) refreshChoices(form);
  if (event.target.matches('[data-select-page]')) form.querySelectorAll('input[name="item"]').forEach(input => { input.checked = event.target.checked; });
  if (form.matches('[data-track-form]')) refreshTracks(form);
});
document.body.addEventListener('input', event => {
  if (!event.target.matches('[data-filter-choices]')) return;
  const group = event.target.closest('[data-choice-group]');
  const query = event.target.value.toLocaleLowerCase().trim();
  let visible = 0;
  group.querySelectorAll('.choice').forEach(choice => { choice.hidden = !choice.textContent.toLocaleLowerCase().includes(query); if (!choice.hidden) visible++; });
  group.querySelector('.choice-empty').hidden = visible > 0;
});
document.body.addEventListener('click', event => {
  if(event.target.closest('[data-example-vibe]')) {const field=event.target.closest('form').querySelector('[name=vibe]');field.value=field.placeholder;field.focus();}
  const button = event.target.closest('[data-choose]');
  if (!button) return;
  const group = button.closest('[data-choice-group]');
  group.querySelectorAll('.choice:not([hidden]) input').forEach(input => { input.checked = button.dataset.choose === 'all'; });
  refreshChoices(button.closest('form'));
});
initialise();

document.body.addEventListener('click', event => {
 const cancel = event.target.closest('[data-close-dialog]');
 if (cancel && cancel.closest('dialog[open]')) { event.preventDefault(); event.stopPropagation(); cancel.closest('dialog').close(); }
 if (event.target.closest('[data-keep-editing]')) document.getElementById('unsaved-dialog').close();
 if (event.target.closest('[data-discard-changes]')) { const proceed = pendingNavigation; pendingNavigation = null; document.getElementById('unsaved-dialog').close(); proceed?.(); }
}, true);
document.getElementById('unsaved-dialog').addEventListener('close', () => { pendingNavigation = null; });

// Audio belongs to the stable shell; HTMX only replaces the main workspace.
const radio = document.getElementById('station-audio');
const player = document.getElementById('radio-player');
let selectedRadio = null;
let radioMetadataEvents=null;
let switchingRadio = false;
function stopRadio() {
  selectedRadio = null;
  radioMetadataEvents?.close();radioMetadataEvents=null;
  radio.pause(); radio.removeAttribute('src'); radio.load();
  player.hidden = true;
}
async function playRadio(button) {
  switchingRadio = true;
  player.querySelector('[data-toggle-radio]').disabled=true;
  player.querySelector('[data-toggle-radio]').textContent='Pause';
  radio.pause(); radio.removeAttribute('src'); radio.load();
  const streamURL = new URL(button.dataset.listenRadio, location.href);
  const listenerID = Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('');
  streamURL.searchParams.set('listener', listenerID);
  selectedRadio = {url:streamURL.href,name:button.dataset.stationName,id:button.dataset.stationId};
  startRadioMetadata();
  document.getElementById('player-station').textContent = selectedRadio.name;
  document.getElementById('player-status').textContent = 'Connecting…';
  player.hidden = false;
  radio.src = selectedRadio.url;
  try { await radio.play(); } catch (error) { if(selectedRadio) document.getElementById('player-status').textContent = 'Unable to listen. Check the station playlist and source, then try again.'; }
  switchingRadio = false;
  player.querySelector('[data-toggle-radio]').disabled=false;
}
document.body.addEventListener('click',event=>{
  const listen=event.target.closest('[data-listen-radio]');
  if(listen && radio) playRadio(listen);
  if(event.target.closest('[data-stop-radio]') && radio) stopRadio();
  if(event.target.closest('[data-toggle-radio]') && selectedRadio) {if(radio.paused || radio.error){const control=player.querySelector('[data-toggle-radio]');switchingRadio=true;control.disabled=true;document.getElementById('player-status').textContent='Connecting…';radio.src=selectedRadio.url;radio.play().catch(()=>{if(selectedRadio)document.getElementById('player-status').textContent='Unable to listen. Try Listen again.'}).finally(()=>{switchingRadio=false;control.disabled=false;});}else radio.pause();}
  const copy=event.target.closest('[data-copy-stream]');
  if(copy) {
    const field=copy.closest('.stream-access').querySelector('input');
    const status=copy.closest('.stream-access').querySelector('[data-copy-status]');
    if(navigator.clipboard) navigator.clipboard.writeText(copy.dataset.copyStream).then(()=>{status.textContent='URL copied.'}).catch(()=>{field.select();status.textContent='Select and copy the URL above.'});
    else {field.select();status.textContent='Select and copy the URL above.'}
  }
});
if(radio) {
  document.getElementById('radio-volume').addEventListener('input',event=>{radio.volume=Number(event.target.value);});
  radio.addEventListener('playing',()=>{document.getElementById('player-status').textContent='Live';player.querySelector('[data-toggle-radio]').textContent='Pause';});
  radio.addEventListener('waiting',()=>{if(selectedRadio)document.getElementById('player-status').textContent='Buffering…'});
  radio.addEventListener('error',()=>{if(selectedRadio){document.getElementById('player-status').textContent='Stream unavailable. Check the source and try again.';player.querySelector('[data-toggle-radio]').textContent='Retry';}});
  radio.addEventListener('ended',()=>{if(selectedRadio){radio.removeAttribute('src');radio.load();player.querySelector('[data-toggle-radio]').textContent='Resume';document.getElementById('player-status').textContent='Stream stopped. Resume to reconnect.';}});
  radio.addEventListener('pause',()=>{
    if(switchingRadio || !selectedRadio || !radio.paused)return;
    // Release the listener connection while retaining the chosen station for Play.
    radio.removeAttribute('src');radio.load();
    document.getElementById('player-status').textContent='Paused';
    player.querySelector('[data-toggle-radio]').textContent='Resume';
  });
  radio.addEventListener('play',()=>{
    if(selectedRadio && !radio.getAttribute('src')){radio.src=selectedRadio.url;radio.play().catch(()=>{});}
  });
}

// Replacement and removal are mutually exclusive actions.
document.body.addEventListener('input',event=>{
 if(event.target.matches('[data-dj-form] input[name="api_key"]') && event.target.value.trim()) {
  const remove=event.target.closest('form').querySelector('input[name="clear_key"]');
  if(remove)remove.checked=false;
 }
});
document.body.addEventListener('change',event=>{
 if(event.target.matches('[data-dj-form] input[name="clear_key"]') && event.target.checked) {
  event.target.closest('form').querySelector('input[name="api_key"]').value='';
 }
});

// Keep the music placeholder if a source has missing or unreadable artwork.
document.body.addEventListener('error',event=>{
 if(event.target.matches?.('img[data-track-artwork]'))event.target.hidden=true;
},true);

// Patch server-rendered card content in place. Unchanged nodes, artwork and
// controls keep their identity, so a listener-count update cannot flash covers.
function nodeKey(node) {
 if(node.nodeType!==Node.ELEMENT_NODE)return String(node.nodeType);
 return node.tagName+':'+(node.id||node.getAttribute('data-station-card')||node.classList[0]||'');
}
function patchStationNode(current,next) {
 if(current.isEqualNode(next))return;
 if(current.nodeType!==Node.ELEMENT_NODE){if(current.nodeValue!==next.nodeValue)current.nodeValue=next.nodeValue;return;}
 if(current.matches('img[data-track-artwork]')&&current.getAttribute('src')===next.getAttribute('src'))return;
 for(const attr of Array.from(current.attributes))if(!next.hasAttribute(attr.name))current.removeAttribute(attr.name);
 for(const attr of Array.from(next.attributes))if(current.getAttribute(attr.name)!==attr.value)current.setAttribute(attr.name,attr.value);
 Array.from(next.childNodes).forEach((child,index)=>{
  let existing=current.childNodes[index];
  if(!existing||nodeKey(existing)!==nodeKey(child)){
   const match=Array.from(current.childNodes).slice(index+1).find(node=>nodeKey(node)===nodeKey(child));
   if(match){current.insertBefore(match,existing||null);existing=match;}
   else{current.insertBefore(child.cloneNode(true),existing||null);return;}
  }
  patchStationNode(existing,child);
 });
 while(current.childNodes.length>next.childNodes.length)current.lastChild.remove();
}
function syncStationEvents(){
 const owner=document.querySelector('#station-cards[data-station-events]');
 if(stationEventOwner===owner)return;
 stationEvents?.close();stationEvents=null;stationEventOwner=owner;
 if(!owner||!window.EventSource)return;
 const connection=new EventSource(owner.dataset.stationEvents);stationEvents=connection;
 connection.addEventListener('stations',event=>{
  if(stationEventOwner!==owner||!owner.isConnected)return;
  const template=document.createElement('template');
  try{template.innerHTML=JSON.parse(event.data);}catch{return;}
  const incoming=template.content.querySelector('#station-cards');if(!incoming)return;
  const active=document.activeElement;
  const activeCard=active?.closest('[data-station-card]')?.dataset.stationCard;
  patchStationNode(owner,incoming);htmx.process(owner);
  if(activeCard&&!active.isConnected){
   Array.from(owner.querySelectorAll('[data-station-card]')).find(card=>card.dataset.stationCard===activeCard)?.querySelector('.card-actions a')?.focus({preventScroll:true});
  }
 });
}
window.addEventListener('pagehide',()=>{stationEvents?.close();stationEvents=null;stationEventOwner=null;});
window.addEventListener('pageshow',syncStationEvents);

function startRadioMetadata(){
 radioMetadataEvents?.close();radioMetadataEvents=null;
 const tracks=document.getElementById('player-tracks');
 tracks.innerHTML='<div class="player-track"><span class="eyebrow">NOW PLAYING</span><strong>Connecting…</strong></div><div class="player-track"><span class="eyebrow">UP NEXT</span><strong>Not queued</strong></div>';
 if(!selectedRadio?.id||!window.EventSource)return;
 const id=selectedRadio.id;
 const connection=new EventSource('/stations/'+encodeURIComponent(id)+'/player');radioMetadataEvents=connection;
 connection.addEventListener('player',event=>{
  if(radioMetadataEvents!==connection||selectedRadio?.id!==id)return;
  const template=document.createElement('template');
  try{template.innerHTML=JSON.parse(event.data);}catch{return;}
  const incoming=template.content.querySelector('#player-tracks');
  if(incoming)patchStationNode(tracks,incoming);
 });
}
window.addEventListener('pagehide',()=>{radioMetadataEvents?.close();radioMetadataEvents=null;});
window.addEventListener('pageshow',()=>{if(selectedRadio)startRadioMetadata();});
