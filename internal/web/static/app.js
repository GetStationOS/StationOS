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
function syncAssistant() {
 const form=document.querySelector('[data-assistant-compose]');
 if (!form) return;
 const status=document.querySelector('[data-assistant-running]');
 const running=status?.dataset.assistantRunning === 'true';
 form.querySelector('button').disabled=form.dataset.assistantEnabled !== 'true' || running;
 document.querySelectorAll('[data-assistant-badge]').forEach(badge => {if (badge.dataset.assistantBadge === status?.dataset.assistantConversation) badge.textContent=running ? 'Working…' : '';});
}
function initialise() {
  syncAssistant();
  discoverFeatureModels();
  showEndpointModels();
  document.querySelectorAll("[data-provider-type]").forEach(select => providerAddressHelp(select,false));
  syncStationEvents();
  document.querySelectorAll('[data-audience-request]').forEach(initialiseAudienceRequest);
  const path = document.querySelector(".screen")?.dataset.page || location.pathname;
  const active = path.startsWith('/producer/assistant') ? 'assistant' : path.startsWith('/producer/djs') ? 'djs' : path.startsWith('/producer/sources') ? 'sources' : path === '/producer/content' && !document.querySelector('.breadcrumbs') ? 'sources' : 'stations';
  document.querySelectorAll('[data-nav]').forEach(link => {
    if (link.dataset.nav === active) link.setAttribute('aria-current', 'page');
    else link.removeAttribute('aria-current');
  });
  document.querySelectorAll('[data-selection-form]').forEach(form => { form.dataset.initial = selectionState(form); refreshChoices(form); });
  document.querySelectorAll('[data-track-form]').forEach(refreshTracks);
  document.querySelectorAll('[data-mixer-form]').forEach(initialiseMixer);
  document.querySelectorAll('.calendar-scroll:not([data-positioned])').forEach(calendar => {
    const cards = Array.from(calendar.querySelectorAll('.calendar-show'));
    if (cards.length) calendar.scrollTop = Math.max(0, Math.min(...cards.map(card => card.offsetTop)) - 106);
    calendar.dataset.positioned = 'true';
  });
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
  const target = event.detail.target || event.detail.elt || event.target;
  if (target.id === 'assistant-messages') { syncAssistant(); return; }
  if (target.id === 'audience-queue-results' || target.id === 'source-discovery-results') return;
  const feedback = document.getElementById('feedback'); feedback.hidden = true;
  if (target.id === 'dialog-body') {
    document.querySelector('#confirm-dialog [data-modal-error]').hidden = true;
    document.getElementById('confirm-dialog').showModal();
    document.querySelector('#confirm-dialog [data-close-dialog]')?.focus();
    return;
  }
  if (target.id === 'main') document.querySelectorAll('dialog[open]').forEach(dialog => dialog.close());
  initialise();
  if (target.id === 'main') {
    document.getElementById('main').focus({preventScroll:true});
  }
  if (target.id === 'contribution-panel' || target.id.startsWith('contribution-')) target.focus({preventScroll:true});
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
document.getElementById('unsaved-dialog')?.addEventListener('close', () => { pendingNavigation = null; });

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
// Supply station branding to browser and phone media controls when supported.
function updateRadioMediaSession() {
 if(!('mediaSession' in navigator)||!window.MediaMetadata)return;
 if(!selectedRadio){navigator.mediaSession.metadata=null;navigator.mediaSession.playbackState='none';return;}
 const tracks=document.getElementById('player-tracks');
 const current=tracks.querySelector('.player-track');
 const title=current?.querySelector('strong')?.textContent.trim();
 const artist=current?.querySelector('.player-track-artist')?.textContent.trim();
 const stationName=document.getElementById('player-station').textContent||selectedRadio.name;
 const logo=document.getElementById('player-logo').getAttribute('src')||'/static/station-logo.jpg';
 navigator.mediaSession.metadata=new MediaMetadata({
  title:title||stationName,artist:artist||stationName,album:stationName,
  artwork:[{src:new URL(logo,location.href).href,type:'image/jpeg'}]
 });
 navigator.mediaSession.playbackState=radio.paused||radio.error||radio.ended?'paused':'playing';
}
function stopRadio() {
  selectedRadio = null;
  radioMetadataEvents?.close();radioMetadataEvents=null;
  radio.pause(); radio.removeAttribute('src'); radio.load();
  player.hidden = true;
  updateRadioMediaSession();
}
async function playRadio(button) {
  switchingRadio = true;
  player.querySelector('[data-toggle-radio]').disabled=true;
  setRadioAction('pause');
  radio.pause(); radio.removeAttribute('src'); radio.load();
  const streamURL = new URL(button.dataset.listenRadio, location.href);
  const listenerID = Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('');
  streamURL.searchParams.set('listener', listenerID);
  selectedRadio = {url:streamURL.href,name:button.dataset.stationName,id:button.dataset.stationId,events:button.dataset.playerEvents};
  startRadioMetadata();
  document.getElementById('player-station').textContent = selectedRadio.name;
  document.getElementById('player-logo').src=button.dataset.stationLogo||'/static/station-logo.jpg';
  document.getElementById('player-status').textContent = 'Connecting…';
  updateRadioMediaSession();
  player.hidden = false;
  radio.src = selectedRadio.url;
  try { await radio.play(); } catch (error) { if(selectedRadio) { document.getElementById('player-status').textContent = 'Unable to listen. Please try again shortly.'; setRadioAction('retry'); } }
  switchingRadio = false;
  player.querySelector('[data-toggle-radio]').disabled=false;
}
document.body.addEventListener('click',event=>{
  const listen=event.target.closest('[data-listen-radio]');
  if(listen && radio) playRadio(listen);
  if(event.target.closest('[data-stop-radio]') && radio) stopRadio();
  if(event.target.closest('[data-toggle-radio]') && selectedRadio) {if(radio.paused || radio.error){const control=player.querySelector('[data-toggle-radio]');switchingRadio=true;control.disabled=true;setRadioAction('pause');document.getElementById('player-status').textContent='Connecting…';radio.src=selectedRadio.url;radio.play().catch(()=>{if(selectedRadio){document.getElementById('player-status').textContent='Unable to listen. Try again.';setRadioAction('retry');}}).finally(()=>{switchingRadio=false;control.disabled=false;});}else radio.pause();}
  const copy=event.target.closest('[data-copy-stream]');
  if(copy) copyStreamURL(copy);
});
if(radio) {
  if('mediaSession' in navigator){
   const actions={
    play:()=>{if(selectedRadio&&(radio.paused||radio.error))player.querySelector('[data-toggle-radio]').click();},
    pause:()=>{if(selectedRadio&&!radio.paused)radio.pause();},
    stop:()=>{if(selectedRadio)stopRadio();}
   };
   for(const [action,handler] of Object.entries(actions)){
    try{navigator.mediaSession.setActionHandler(action,handler);}catch{/* Some browsers support only a subset of media actions. */}
   }
  }
  document.getElementById('radio-volume').addEventListener('input',event=>{radio.volume=Number(event.target.value);});
  radio.addEventListener('playing',()=>{document.getElementById('player-status').textContent='Live';setRadioAction('pause');updateRadioMediaSession();});
  radio.addEventListener('waiting',()=>{if(selectedRadio)document.getElementById('player-status').textContent='Buffering…'});
  radio.addEventListener('error',()=>{if(selectedRadio){document.getElementById('player-status').textContent='Stream unavailable. Please try again shortly.';setRadioAction('retry');updateRadioMediaSession();}});
  radio.addEventListener('ended',()=>{if(selectedRadio){radio.removeAttribute('src');radio.load();setRadioAction('play');document.getElementById('player-status').textContent='Stream stopped. Resume to reconnect.';updateRadioMediaSession();}});
  radio.addEventListener('pause',()=>{
    if(switchingRadio || !selectedRadio || !radio.paused)return;
    // Release the listener connection while retaining the chosen station for Play.
    radio.removeAttribute('src');radio.load();
    document.getElementById('player-status').textContent='Paused';
    setRadioAction('play');
    updateRadioMediaSession();
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
 const connection=new EventSource(selectedRadio.events||'/producer/stations/'+encodeURIComponent(id)+'/player');radioMetadataEvents=connection;
 connection.addEventListener('unavailable',()=>{
  if(radioMetadataEvents!==connection)return;
  stopRadio();showError('This station is no longer available to the public.');
 });
 connection.addEventListener('player',event=>{
  if(radioMetadataEvents!==connection||selectedRadio?.id!==id)return;
  const template=document.createElement('template');
  try{template.innerHTML=JSON.parse(event.data);}catch{return;}
  const incoming=template.content.querySelector('#player-tracks');
  if(incoming){
   patchStationNode(tracks,incoming);
   const logo=document.getElementById('player-logo');
   if(logo.getAttribute('src')!==incoming.dataset.stationLogo)logo.src=incoming.dataset.stationLogo;
   document.getElementById('player-station').textContent=incoming.dataset.stationName;
   updateRadioMediaSession();
  }
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
 if(!('savedSrc' in image.dataset)){image.dataset.savedSrc=image.getAttribute('src')||'';image.dataset.savedRemovable=String(!form.querySelector('[data-remove-photo]').hidden);}
 if(image.dataset.previewUrl){URL.revokeObjectURL(image.dataset.previewUrl);delete image.dataset.previewUrl;}
 form.querySelector('[data-photo-removed]').value=removed?'yes':'';
 if(removed)input.value='';
 const file=input.files[0];
 const src=removed?(image.dataset.defaultSrc||''):file?URL.createObjectURL(file):image.dataset.savedSrc;
 if(file&&src)image.dataset.previewUrl=src;
 if(src)image.src=src;else image.removeAttribute('src');
 image.hidden=!src;
 form.querySelector('[data-photo-placeholder]').hidden=!!src;
 form.querySelector('[data-remove-photo]').hidden=!src||!!image.dataset.defaultSrc&&(removed||!file&&image.dataset.savedRemovable!=='true');
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

// Public programme updates preserve unchanged artwork and focused controls.
let publicEvents=null;
let publicEventOwner=null;
function syncPublicEvents(){
 const owner=document.querySelector('[data-public-events]');
 if(publicEventOwner===owner)return;
 publicEvents?.close();publicEvents=null;publicEventOwner=owner;
 if(!owner||!window.EventSource)return;
 const connection=new EventSource(owner.dataset.publicEvents);publicEvents=connection;
 connection.addEventListener('public',event=>{
  if(publicEventOwner!==owner||!owner.isConnected)return;
  const template=document.createElement('template');
  try{template.innerHTML=JSON.parse(event.data);}catch{return;}
  const incoming=template.content.querySelector('#public-live');
  if(incoming)patchStationNode(owner,incoming);
 });
 connection.addEventListener('unavailable',()=>{
  connection.close();owner.textContent='This station is no longer available to the public.';
 });
}
document.body.addEventListener('htmx:afterSettle',syncPublicEvents);
window.addEventListener('pagehide',()=>{publicEvents?.close();publicEvents=null;publicEventOwner=null;});
window.addEventListener('pageshow',syncPublicEvents);
syncPublicEvents();

// Smooth only the show region being updated, without scrolling or replacing
// neighbouring shows. Native details retain their contents when collapsed.
document.body.addEventListener('htmx:beforeSwap', event => {
 const target = event.detail.target;
 if (!event.detail.shouldSwap || !target?.matches('.public-show-body, .show-contribution')) return;
 target.dataset.previousHeight = String(target.getBoundingClientRect().height);
});
document.body.addEventListener('htmx:afterSwap', event => {
 const target = event.detail.target;
 if (!target?.matches('.public-show-body, .show-contribution')) return;
 const previous = Number(target.dataset.previousHeight);
 delete target.dataset.previousHeight;
 if (!previous || window.matchMedia('(prefers-reduced-motion: reduce)').matches || !target.animate) return;
 const current = target.getBoundingClientRect().height;
 target.animate([{height:previous+'px',overflow:'hidden',opacity:0.7},{height:current+'px',overflow:'hidden',opacity:1}], {duration:180,easing:'ease-out'});
});

// Keep the day controls mounted and update their selection from the server's
// rendered day, while HTMX swaps only the programme underneath them.
document.body.addEventListener('htmx:afterSwap', event => {
 if (event.detail.target?.id !== 'public-day-programme') return;
 const day=document.getElementById('public-day-programme')?.dataset.programmeDay;
 document.querySelectorAll('.public-day-tabs a').forEach(link => {
  if (new URL(link.href).searchParams.get('day')===day) link.setAttribute('aria-current','date');
  else link.removeAttribute('aria-current');
 });
});

// Reopening a loaded show keeps the form mounted and avoids another fetch.
document.body.addEventListener('htmx:beforeRequest', event => {
 const trigger=event.detail.elt;
 if (!trigger?.matches('[data-open-show]')) return;
 const card=trigger.closest('.public-show-card');
 if (trigger.getAttribute('aria-expanded')==='true') {
  card.querySelector('.public-show-body').hidden=true;
  card.classList.remove('show-open');
  trigger.setAttribute('aria-expanded','false');
  event.preventDefault();
  return;
 }
 card.querySelector('.public-show-body').hidden=false;
 card.classList.add('show-open');
 trigger.setAttribute('aria-expanded','true');
 if (card.dataset.showLoaded==='true') event.preventDefault();
});
document.body.addEventListener('htmx:afterSwap', event => {
 const target=event.detail.target;
 if (target?.matches('.public-show-body')) target.closest('.public-show-card').dataset.showLoaded='true';
});

// Keep the chosen recording and other listener fields outside the search swap.
function selectAudienceRecording(form, radio) {
 const track=form.querySelector('[data-selected-track]');
 track.value=radio.value;
 const summary=form.querySelector('[data-selected-recording]');
 summary.querySelector('[data-selected-title]').textContent=radio.dataset.title;
 summary.querySelector('[data-selected-artist]').textContent=[radio.dataset.artist,radio.dataset.album].filter(Boolean).join(' · ');
 summary.hidden=false;
 form.querySelector('.audience-submit').disabled=false;
}
function initialiseAudienceRequest(form) {
 const track=form.querySelector('[data-selected-track]');
 const radios=form.querySelectorAll('[data-track-result]');
 radios.forEach(radio => {
  radio.name='track-choice'; radio.required=false;
  if (!track.value && radio.checked) selectAudienceRecording(form,radio);
  radio.checked=radio.value===track.value;
  if (radio.checked) selectAudienceRecording(form,radio);
 });
 track.disabled=false;
 form.querySelector('.audience-submit').disabled=!track.value;
 form.closest('.show-contribution,.public-participation').querySelector('[data-track-search-submit]').hidden=true;
}
document.body.addEventListener('change', event => {
 const radio=event.target;
 if (radio.matches('[data-track-result]')) selectAudienceRecording(radio.closest('form'),radio);
});
document.body.addEventListener('htmx:beforeSwap', event => {
 const target=event.detail.target;
 if (!target?.matches('[data-audience-track-results]')) return;
 const panel=target.closest('.show-contribution,.public-participation');
 if (!panel?.isConnected) { event.detail.shouldSwap=false; return; }
 const query=panel.querySelector('.audience-track-search input[name=q]').value.trim();
 const response=new DOMParser().parseFromString(event.detail.serverResponse,'text/html');
 const results=response.querySelector('[data-audience-track-results]');
 // Ignore an older response if the listener has already changed the search.
 if (results && results.dataset.query!==query) event.detail.shouldSwap=false;
});

async function copyStreamURL(control) {
 const card=control.closest('.stream-access');
 const status=card.querySelector('[data-copy-status]');
 control.classList.remove('is-copied');
 status.textContent='Copying…';
 control.disabled=true;
 try {
  if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable');
  await navigator.clipboard.writeText(control.dataset.copyStream);
  control.classList.add('is-copied');
  status.textContent='Copied to clipboard.';
 } catch {
  const input=card.querySelector('input');
  if (input) { input.focus({preventScroll:true}); input.select(); }
  else {
   const address=control.querySelector('[data-stream-address]');
   const range=document.createRange(); range.selectNodeContents(address);
   const selection=window.getSelection(); selection.removeAllRanges(); selection.addRange(range);
  }
  status.textContent='Unable to copy automatically. The address is selected—use your device’s Copy command.';
 } finally { control.disabled=false; }
}

// Queue refreshes retain the column being read, including on touch screens.
const audienceScrollPositions=new WeakMap();
document.body.addEventListener('htmx:beforeSwap', event => {
 const target=event.detail.target;
 if (target?.id!=='audience-queue-results') return;
 const automatic=event.detail.requestConfig?.elt?.id==='audience-queue-results';
 if (automatic && target.contains(document.activeElement)) {event.detail.shouldSwap=false;return;}
 const scroller=target.querySelector('.table-scroll');
 if (scroller && event.detail.xhr) audienceScrollPositions.set(event.detail.xhr,scroller.scrollLeft);
});
document.body.addEventListener('htmx:afterSwap', event => {
 if (!event.detail.xhr || !audienceScrollPositions.has(event.detail.xhr)) return;
 const scroller=document.querySelector('#audience-queue-results .table-scroll');
 if (scroller) scroller.scrollLeft=audienceScrollPositions.get(event.detail.xhr);
 audienceScrollPositions.delete(event.detail.xhr);
});

// A failed status refresh must not leave a stale Working label looking authoritative.
for (const eventName of ['htmx:responseError', 'htmx:sendError', 'htmx:timeout']) {
  document.addEventListener(eventName, event => {
    if (event.detail?.elt?.id === 'assistant-messages') {
      const warning = document.querySelector('.assistant-poll-error');
      if (warning) warning.hidden = false;
    }
  });
}

// Provider changes offer a matching address while preserving custom server values.
function providerAddressHelp(select, changed) {
 const defaults={ollama:'http://your-server:11434',compatible:'https://your-provider/v1',openai:'https://api.openai.com/v1',anthropic:'https://api.anthropic.com/v1',gemini:'https://generativelanguage.googleapis.com/v1beta'};
 const field=select.form?.querySelector('[name="address"]');
 if (!field) return;
 field.placeholder=defaults[select.value];
 if (changed && (!field.value || Object.values(defaults).includes(field.value)) && ['openai','anthropic','gemini'].includes(select.value)) field.value=defaults[select.value];
 const help=select.form.querySelector('[data-provider-address-help]');
 if (help) help.textContent=(select.value==='ollama'?'Use the server address without /api. ':select.value==='compatible'?'Use the API base, usually ending /v1. ':'Use the provider API address shown above. ')+'The address must be reachable from StationOS. Localhost refers to its computer or container.';
}
document.addEventListener('change',event=>{if(event.target.matches('[data-provider-type]'))providerAddressHelp(event.target,true);});

async function endpointModels(url, options) {
 const response=await fetch(url,options);
 const result=await response.json().catch(()=>({error:'Could not discover models. Check the endpoint details and try again.'}));
 if (!response.ok) throw new Error(result.error || 'Could not discover models. Enter a model ID manually.');
 return result.models || [];
}
function discoverFeatureModels() {
 const requests=new Map();
 document.querySelectorAll('[data-feature-provider]').forEach(select=>{
  const id=select.value, role=select.dataset.featureProvider;
  const list=document.getElementById('models-'+role);
  const status=document.querySelector('[data-feature-model-status="'+role+'"]');
  if (!list || !status) return;
  list.replaceChildren();status.textContent='Finding available models…';
  const picker=document.querySelector('[data-model-picker="'+role+'"]'),choice=document.querySelector('[data-model-choice="'+role+'"]'),manual=document.querySelector('[data-model-manual="'+role+'"]'),input=document.querySelector('[data-feature-model="'+role+'"]');
  picker.hidden=true;manual.hidden=false;
  if (!requests.has(id)) requests.set(id,endpointModels('/producer/assistant/providers/'+encodeURIComponent(id)+'/models'));
  requests.get(id).then(models=>{
   if (!select.isConnected || select.value!==id) return;
   models.forEach(model=>{const option=document.createElement('option');option.value=model;list.append(option);});
   if (models.length) {
    choice.replaceChildren();
    const values=models.includes(input.value)?models:[input.value,...models];
    values.forEach(model=>{if(!model)return;const option=document.createElement('option');option.value=model;option.textContent=model;option.selected=model===input.value;choice.append(option);});
    const custom=document.createElement('option');custom.value='__manual';custom.textContent='Enter a model ID…';choice.append(custom);
    picker.hidden=false;manual.hidden=!!input.value;if(!input.value)choice.value='__manual';
   }
   status.textContent=models.length ? models.length+' models available.' : 'No models listed. Enter a model ID manually.';
  }).catch(error=>{if(select.isConnected && select.value===id)status.textContent=error.message;});
 });
}
document.addEventListener('change',event=>{if(event.target.matches('[data-feature-provider]'))discoverFeatureModels();});
function showEndpointModels() {
 const region=document.querySelector('[data-endpoint-models]');if(!region)return;
 const status=region.querySelector('[data-model-status]'),list=region.querySelector('[data-model-list]');
 status.textContent='Finding available models…';
 endpointModels('/producer/assistant/providers/'+encodeURIComponent(region.dataset.savedEndpoint)+'/models').then(models=>{
  if(!region.isConnected)return;
  models.forEach(model=>{const item=document.createElement('li');item.textContent=model;list.append(item);});list.hidden=!models.length;
  status.textContent=models.length ? models.length+' models available.' : 'Endpoint connected. No models are listed.';
 }).catch(error=>{if(region.isConnected)status.textContent=error.message;});
}

document.addEventListener('change',event=>{
 const choice=event.target.closest('[data-model-choice]');if(!choice)return;
 const role=choice.dataset.modelChoice,input=document.querySelector('[data-feature-model="'+role+'"]'),manual=document.querySelector('[data-model-manual="'+role+'"]');
 manual.hidden=choice.value!=='__manual';
 if(choice.value==='__manual')input.focus();else input.value=choice.value;
});

// Selecting a discovered server fills only the creation form. Ordinary GET
// forms provide the same prefilled screen when JavaScript is unavailable.
document.body.addEventListener('submit', event => {
 const choice=event.target.closest('[data-source-choice]');
 if (!choice) return;
 const form=document.querySelector('[data-new-source]');
 if (!form) return;
 event.preventDefault();
 event.stopImmediatePropagation();
 for (const name of ['name','endpoint']) {
  const field=form.elements.namedItem(name);
  field.value=choice.elements.namedItem(name).value;
  field.dispatchEvent(new Event('input',{bubbles:true}));
 }
 document.querySelectorAll('[data-source-choice] button').forEach(button => {
  const selected=button.form===choice;
  button.setAttribute('aria-pressed',String(selected));
  button.textContent=selected ? 'Selected' : 'Use server';
 });
 form.querySelector('[data-source-selected]').textContent='Server selected. Review its details, then add the source.';
 form.elements.namedItem('name').focus({preventScroll:true});
}, true);
