const mixerSaves = new WeakMap();
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
  document.querySelectorAll('[data-mixer-form]').forEach(initialiseMixer);
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
function mixerState(form) { return JSON.stringify(Array.from(form.querySelectorAll('[data-mixer-level]'), field => [field.name, field.value])); }
function refreshMixer(form) {
 form.querySelectorAll('[data-volume-for]').forEach(slider => {
  const field = document.getElementById(slider.dataset.volumeFor);
  slider.value = field.value;
  slider.style.setProperty('--fader-fill', (Number(slider.value) / 4) + '%');
  slider.setAttribute('aria-valuetext', slider.value + ' percent');
  const mute = form.querySelector('[data-mixer-mute="'+field.id+'"]');
  const muted = field.value !== '' && Number(field.value) === 0;
  mute.setAttribute('aria-pressed', String(muted));
  mute.textContent = muted ? 'Unmute' : 'Mute';
  mute.setAttribute('aria-label', (muted ? 'Unmute ' : 'Mute ') + field.dataset.channelName);
 });

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
function setRadioAction(action) {
  const control = player.querySelector('[data-toggle-radio]');
  const label = {pause:'Pause',play:'Resume',retry:'Retry'}[action];
  control.dataset.radioAction = action;
  control.setAttribute('aria-label', label);
  control.title = label;
}
function stopRadio() {
  selectedRadio = null;
  radioMetadataEvents?.close();radioMetadataEvents=null;
  radio.pause(); radio.removeAttribute('src'); radio.load();
  player.hidden = true;
}
async function playRadio(button) {
  switchingRadio = true;
  player.querySelector('[data-toggle-radio]').disabled=true;
  setRadioAction('pause');
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
  try { await radio.play(); } catch (error) { if(selectedRadio) { document.getElementById('player-status').textContent = 'Unable to listen. Check the station playlist and source, then try again.'; setRadioAction('retry'); } }
  switchingRadio = false;
  player.querySelector('[data-toggle-radio]').disabled=false;
}
document.body.addEventListener('click',event=>{
  const listen=event.target.closest('[data-listen-radio]');
  if(listen && radio) playRadio(listen);
  if(event.target.closest('[data-stop-radio]') && radio) stopRadio();
  if(event.target.closest('[data-toggle-radio]') && selectedRadio) {if(radio.paused || radio.error){const control=player.querySelector('[data-toggle-radio]');switchingRadio=true;control.disabled=true;setRadioAction('pause');document.getElementById('player-status').textContent='Connecting…';radio.src=selectedRadio.url;radio.play().catch(()=>{if(selectedRadio){document.getElementById('player-status').textContent='Unable to listen. Try again.';setRadioAction('retry');}}).finally(()=>{switchingRadio=false;control.disabled=false;});}else radio.pause();}
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
  radio.addEventListener('playing',()=>{document.getElementById('player-status').textContent='Live';setRadioAction('pause');});
  radio.addEventListener('waiting',()=>{if(selectedRadio)document.getElementById('player-status').textContent='Buffering…'});
  radio.addEventListener('error',()=>{if(selectedRadio){document.getElementById('player-status').textContent='Stream unavailable. Check the source and try again.';setRadioAction('retry');}});
  radio.addEventListener('ended',()=>{if(selectedRadio){radio.removeAttribute('src');radio.load();setRadioAction('play');document.getElementById('player-status').textContent='Stream stopped. Resume to reconnect.';}});
  radio.addEventListener('pause',()=>{
    if(switchingRadio || !selectedRadio || !radio.paused)return;
    // Release the listener connection while retaining the chosen station for Play.
    radio.removeAttribute('src');radio.load();
    document.getElementById('player-status').textContent='Paused';
    setRadioAction('play');
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
 tracks.querySelectorAll('img[data-track-artwork], .player-track-artist').forEach(node=>node.remove());
 const rows=tracks.querySelectorAll('.player-track');
 rows[0].querySelector('.eyebrow').textContent='NOW PLAYING';
 rows[0].querySelector('strong').textContent='Connecting…';
 rows[1].querySelector('strong').textContent='Not queued';
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

document.body.addEventListener('input', event => {
 const form = event.target.closest('[data-mixer-form]');
 if (!form) return;
 if (event.target.matches('[data-volume-for]')) {
  document.getElementById(event.target.dataset.volumeFor).value = event.target.value;
 } else if (event.target.type === 'number') {
  const slider = form.querySelector('[data-volume-for="'+event.target.id+'"]');
  if (slider) slider.value = event.target.value;
 }
 refreshMixer(form);
 saveMixer(form);
});

document.body.addEventListener('click', event => {
 const button = event.target.closest('[data-mixer-mute], [data-mixer-reset]');
 if (!button) return;
 const form = button.closest('[data-mixer-form]');
 const field = document.getElementById(button.dataset.mixerMute || button.dataset.mixerReset);
 if (button.hasAttribute('data-mixer-reset')) {
  field.value = '100';
 } else if (Number(field.value) === 0) {
  field.value = field.dataset.unmutedLevel || '100';
 } else {
  field.dataset.unmutedLevel = field.value;
  field.value = '0';
 }
 refreshMixer(form);
 saveMixer(form);
});

function initialiseMixer(form) {
 if (mixerSaves.has(form)) return;
 mixerSaves.set(form, {saved: mixerState(form), saving: false});
 form.querySelectorAll('[data-fader], [data-mixer-actions]').forEach(control => { control.hidden = false; });
 refreshMixer(form);
 form.addEventListener('submit', event => { event.preventDefault(); saveMixer(form); });
}
function mixerFeedback(form, message, failed = false) {
 const output = form.querySelector('[data-mixer-feedback]');
 output.textContent = message;
 output.setAttribute('role', failed ? 'alert' : 'status');
 form.querySelector('[data-mixer-retry]').hidden = !failed;
}
async function saveMixer(form) {
 const state = mixerSaves.get(form);
 if (!state || state.saving) return;
 if (!form.checkValidity()) {
  mixerFeedback(form, 'Enter a whole-number level from 0% to 400%.');
  return;
 }
 const snapshot = mixerState(form);
 if (snapshot === state.saved) { mixerFeedback(form, 'Saved'); return; }
 const body = new URLSearchParams(new FormData(form));
 state.saving = true;
 mixerFeedback(form, 'Saving…');
 try {
  const response = await fetch(form.action, {
   method: 'POST', body, credentials: 'same-origin', keepalive: true,
   headers: {'X-Mixer-Autosave': 'true', 'Accept': 'application/json'}
  });
  const result = await response.json().catch(() => null);
  if (!response.ok || result?.status !== 'saved') {
   throw new Error(result?.error || (response.status === 401 ? 'Reload to sign in before adjusting the mixer.' : 'Couldn’t save the mixer. Please retry.'));
  }
  state.saved = snapshot;
  mixerFeedback(form, 'Saved');
 } catch (error) {
  // A lost response may follow a committed write; retry the desired state.
  state.saved = null;
  mixerFeedback(form, error.message || 'Couldn’t save the mixer. Please retry.', true);
 } finally {
  state.saving = false;
  // Serialize requests and send the latest position after an earlier save.
  // HTMX navigation may detach the form; its pending save still completes.
  if (mixerState(form) !== snapshot) saveMixer(form);
 }
}
document.body.addEventListener('click', event => {
 const retry = event.target.closest('[data-mixer-retry]');
 if (retry) saveMixer(retry.closest('[data-mixer-form]'));
});

// Retain unchanged covers when the station Overview refreshes its live state.
document.body.addEventListener('htmx:beforeSwap',event=>{
 const target=event.detail.target;
 if(target?.id!=='station-live'||!event.detail.shouldSwap||event.detail.xhr.status!==200)return;
 const incoming=target.cloneNode(false);
 incoming.innerHTML=event.detail.serverResponse;
 patchStationNode(target,incoming);
 event.detail.shouldSwap=false;
});

// Delegation keeps the speed display working after HTMX navigation.
document.body.addEventListener('input', event => {
 if(event.target.matches('[data-conversation-speed]')) {
  event.target.closest('.speed-control').querySelector('[data-speed-value]').textContent = Number(event.target.value).toFixed(2) + '×';
 }
});

// Photo changes remain local until the profile is saved.
function previewDJPhoto(form, removed) {
 const input=form.querySelector('[data-dj-photo]');
 const image=form.querySelector('[data-photo-preview]');
 if(!('savedSrc' in image.dataset))image.dataset.savedSrc=image.getAttribute('src')||'';
 if(image.dataset.previewUrl){URL.revokeObjectURL(image.dataset.previewUrl);delete image.dataset.previewUrl;}
 form.querySelector('[data-photo-removed]').value=removed?'yes':'';
 if(removed)input.value='';
 const file=input.files[0];
 const src=removed?'':file?URL.createObjectURL(file):image.dataset.savedSrc;
 if(file&&src)image.dataset.previewUrl=src;
 if(src)image.src=src;else image.removeAttribute('src');
 image.hidden=!src;
 form.querySelector('[data-photo-placeholder]').hidden=!!src;
 form.querySelector('[data-remove-photo]').hidden=!src;
}
document.body.addEventListener('change',event=>{
 if(event.target.matches('[data-dj-photo]'))previewDJPhoto(event.target.closest('[data-dj-form]'),false);
});
document.body.addEventListener('click',event=>{
 const remove=event.target.closest('[data-remove-photo]');
 if(!remove)return;
 event.preventDefault();
 previewDJPhoto(remove.closest('[data-dj-form]'),true);
 remove.closest('[data-dj-form]').querySelector('[data-dj-photo]').focus();
});
document.body.addEventListener('htmx:beforeCleanupElement',event=>{
 event.detail.elt.querySelectorAll?.('[data-photo-preview]').forEach(image=>{if(image.dataset.previewUrl)URL.revokeObjectURL(image.dataset.previewUrl);});
});
