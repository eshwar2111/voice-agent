// internal/ui/assets/js/main.js
// Entry point for the overlay page. ES modules do not create globals, so every
// function referenced from a markup `onclick=`/`oninput=`/`onkeydown=` attribute
// (static or built into an innerHTML string) — and every function Go calls
// directly via webview Eval (bare, not through window.__agent.recv) — must be
// assigned to `window` explicitly below.

import { resolve, PRESENCE_SIZES } from './state.js';
import { morphTo, swapContent } from './motion.js';

/* ─── logging: everything goes to Go (voice-agent.log) ────────────────────── */
function jlog(m){try{window.jslog&&window.jslog('[js] '+m)}catch(e){}}

/* Single entry point for everything Go pushes. Handlers are registered by
   feature modules; unknown kinds are logged and dropped, never thrown, so a
   stale Go build can't take down the render loop. */
window.__agent = {
  handlers: {},
  on(kind, fn){ (this.handlers[kind] ||= []).push(fn) },
  recv(env){
    try{
      const hs = this.handlers[env.kind];
      if(!hs || !hs.length){ jlog('unhandled event '+env.kind); return }
      for(const h of hs) h(env.data);
    }catch(e){ jlog('recv error '+env.kind+': '+e) }
  }
};
window.__agent.on('state', d => updateUI(d.state, d.text));
window.__agent.on('notify', d => updateUI('idle', d.text));
window.__agent.on('surface:open', d => {
  if(d.id==='command') showCommand();
  else if(d.id==='result') showCard(d.text);
  else if(d.id==='approve'){
    /* Explicit discriminator, not JSON.parse sniffing: RequestConfirmationCard
       sends {card}, RequestConfirmation sends {text}. Never infer structure
       from string contents on the security-approval path. If neither is
       present (shouldn't happen from either current caller), refuse to open
       an unreadable approval panel rather than rendering the literal word
       "undefined" for the user to approve blind. */
    if(d.card!==undefined) showConfirmCard(d.card);
    else if(d.text!==undefined) showConfirm(d.text);
    else { jlog('approve payload missing card and text'); resolveConfirm(false); }
  }
});
window.__agent.on('activity:update', d => {
  if(d.id==='ambient.nudge')
    showSuggestion(d.data.id, d.data.icon, d.data.title, d.data.message, d.data.action);
});

let currentPanel=null,latestOutput='',integrationTimer=null;
let settingsState={llm_provider:'gemini',api_key:'',model:'',base_url:'',enable_voice:false,privacy_mode:false};
const dashCopy={overview:['Overview','Your assistant at a glance.','See readiness, wire in ecosystems, and tune behavior.'],integrations:['Integrations','Connected ecosystems that feel first-class.','Google Workspace and Spotify behave like native assistant surfaces.'],model:['Models','Shape how the assistant thinks.','Point the runtime at the provider and model that fit your setup.'],privacy:['Privacy','Tune interaction and context posture.','Decide how voice and privacy defaults should feel.']};
function esc(t){return String(t).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#39;')}

/* Publish every visible surface to Go, which unions them into the window
   region. Anything not covered by these rects is not part of the window at all,
   so clicks there land on the desktop. Called on visibility changes, and on
   morph start/settle from the island's render loop — never per animation frame. */
function publishRegionRects(){
  const rects=[];
  document.querySelectorAll('#island, .panel.active, .card.shown, #dashboard.visible').forEach(el=>{
    const cs=getComputedStyle(el);
    if(cs.display==='none') return;
    const r=el.getBoundingClientRect();
    if(r.width>0 && r.height>0) rects.push({x:r.left,y:r.top,w:r.width,h:r.height});
  });
  jlog('regionRects '+rects.length);
  window.setRegionRects && window.setRegionRects(rects);
}
function sizeTo(_v){ publishRegionRects(); }
function toast(t){const el=document.getElementById('toast');el.textContent=t;el.style.display='block';clearTimeout(toast.tm);toast.tm=setTimeout(()=>el.style.display='none',2200)}

/* ─── island store + render loop ──────────────────────────────────────────── */
const island = document.getElementById('island');
const islandBody = document.getElementById('islandBody');
const capLead = document.getElementById('capLead');
const capTrail = document.getElementById('capTrail');

const store = { surface:null, activities:[], agentState:'idle',
                hover:false, idleSince:Date.now(), now:Date.now(), stateText:null };
let applied = { presence:null, contentId:null };

/* The region must never be smaller than what CSS is painting at any instant of
   a morph, or the island gets visibly clipped mid-animation. Rather than
   animating the region in lockstep (which needs per-frame IPC or the easing
   curve duplicated in Go), publish the BOUNDING BOX of the from- and to-shapes
   for the duration, then the exact shape once it settles. Two calls, not sixty.

   Cost: for the ~460ms of a grow, the surplus area is transparent window that
   eats clicks. Bounded, brief, and confined to where the island is about to be. */
function unionRegionRect(fromPresence, toPresence){
  const a = PRESENCE_SIZES[fromPresence], b = PRESENCE_SIZES[toPresence];
  if(!a || !b) return null;
  const r = island.getBoundingClientRect();
  const cx = r.left + r.width/2, top = r.top;
  const w = Math.max(a.w, b.w), h = Math.max(a.h, b.h);
  return {x: cx - w/2, y: top, w, h};
}

function defaultLabelFor(state){
  if(state==='listening') return 'Listening…';
  if(state==='thinking') return 'Thinking…';
  if(state==='acting') return 'Working…';
  if(state==='speaking') return 'Speaking…';
  return 'Working…';
}

// Content ids beyond 'idle'/'agent.run' (spotify.nowplaying, trust.approval,
// command/result/controlcenter surfaces, ...) arrive in Task 6/7. Returning an
// empty div rather than throwing keeps the render loop alive if resolve() ever
// names one before its renderer exists.
function renderContentFor(id){
  if(id === 'idle'){
    const d=document.createElement('div');
    d.innerHTML = '<span class="ttl">Ready</span>';
    return d;
  }
  if(id === 'agent.run'){
    const d=document.createElement('div');
    const label = store.stateText || defaultLabelFor(store.agentState);
    d.innerHTML = '<span class="ttl">'+esc(label)+'</span>'+
      '<span class="eq"><i></i><i></i><i></i><i></i><i></i></span>';
    return d;
  }
  return document.createElement('div');
}

function updateCaps(id){
  if(id === 'agent.run'){
    capLead.innerHTML = '<svg class="ico"><use href="#i-wave"/></svg>';
    capTrail.innerHTML = '';
  } else {
    capLead.innerHTML = '<svg class="ico"><use href="#i-mic"/></svg>';
    capTrail.innerHTML = '<svg class="ico"><use href="#i-gear"/></svg>';
  }
}

export function rerender(){
  store.now = Date.now();
  const r = resolve(store);
  if(r.presence !== applied.presence){
    // Widen the region FIRST, so the growing island is never clipped.
    const u = unionRegionRect(applied.presence || r.presence, r.presence);
    if(u) window.setRegionRects && window.setRegionRects([u]);
    // ...then narrow it to the exact shape once the morph settles.
    morphTo(island, r.presence, publishRegionRects);
    applied.presence = r.presence;
  }
  if(r.contentId !== applied.contentId){
    swapContent(islandBody, () => renderContentFor(r.contentId));
    updateCaps(r.contentId);
    applied.contentId = r.contentId;
  }
  setSurface(r.surface);
  publishRegionRects();
}

// Stub in this task; Task 7 replaces it with real surface routing. Defined now
// so rerender() does not throw before surfaces are modularized.
function setSurface(id){
  const dash = document.getElementById('dashboard');
  if(dash) dash.classList.toggle('visible', id === 'controlcenter');
}

// Trailing control on agent.run — hides the progress display without touching
// the running plan (see activities.js note in Task 6). `endActivity` and
// `syncAndRerender` are wired up by Task 6; nothing in this task calls this yet.
window.dismissRunDisplay = () => { endActivity('agent.run', syncAndRerender) };

island.addEventListener('mouseenter', () => { store.hover = true;  rerender() });
island.addEventListener('mouseleave', () => { store.hover = false; rerender() });
setInterval(() => { if(store.agentState==='idle' && !store.activities.length) rerender() }, 1000);

/* ─── island state driven by Go pushes ────────────────────────────────────── */
function handleIslandClick(){
  if(dashboard.classList.contains('visible')) return;
  if(currentPanel){ collapseShell(); return }
  jlog('island click -> triggerListen');
  window.triggerListen && window.triggerListen();
}

function updateUI(state,text){
  jlog('updateUI '+state+(text?(' "'+text+'"'):''));
  // Reset the dormant clock on every transition INTO idle, so a burst of
  // stray idle notifications can't make the island snap to dormant early or
  // late relative to when the agent actually went quiet.
  const enteringIdle = state === 'idle' && store.agentState !== 'idle';
  store.agentState = state;
  store.stateText = text || null;
  if(enteringIdle) store.idleSince = Date.now();
  rerender();
}

function refreshIslandVisibility(){const sg=document.getElementById('suggestion').classList.contains('shown');const hide=!!currentPanel||sg||dashboard.classList.contains('visible');island.style.display=hide?'none':'flex';publishRegionRects()}
function setPanel(id){['commandPanel','outputPanel','confirmPanel'].forEach(x=>document.getElementById(x).classList.remove('active'));currentPanel=id;if(id){document.getElementById(id).classList.add('active')}refreshIslandVisibility();publishRegionRects()}
function collapseShell(){jlog('collapse');setPanel(null);if(window.setInputActive)window.setInputActive(false);sizeTo('pill');if(!dashboard.classList.contains('visible'))updateUI('idle')}
function showCommand(){jlog('showCommand');setPanel('commandPanel');sizeTo('command');if(window.setInputActive)window.setInputActive(true);setTimeout(()=>commandInput.focus(),80)}
function fillSuggestion(v){commandInput.value=v;commandInput.focus()}
function renderText(t){return '<div>'+esc(t).replace(/\n/g,'<br/>').replace(/\*\*(.*?)\*\*/g,'<strong>$1</strong>')+'</div>'}
function renderContent(text){try{const p=JSON.parse(text);if(p&&p.type==='calendar_list'&&Array.isArray(p.data))return '<div class="list">'+p.data.map(x=>'<div class="item"><div class="item-title">'+esc(x.summary||'Untitled event')+'</div><div class="item-meta">'+esc(x.startTime||'')+(x.location?' · '+esc(x.location):'')+'</div></div>').join('')+'</div>';if(p&&p.type==='gmail_list'&&Array.isArray(p.data))return '<div class="list">'+p.data.map(x=>'<div class="item"><div class="item-title">'+esc(x.subject||'No subject')+'</div><div class="item-meta">'+esc(x.from||'Unknown')+(x.date?' · '+esc(x.date):'')+'</div>'+(x.snippet?'<div style="margin-top:8px;color:var(--ink-2);font-size:12px">'+esc(x.snippet)+'</div>':'')+'</div>').join('')+'</div>';if(p&&p.type==='system_status'&&p.data)return '<div class="list"><div class="item"><div class="item-title">CPU</div><div class="item-meta">'+Number(p.data.cpu||0)+'% active</div></div><div class="item"><div class="item-title">Memory</div><div class="item-meta">'+(p.data.ramFree||0)+' GB free of '+(p.data.ramTotal||0)+' GB</div></div></div>'}catch(e){}return renderText(text)}
function showCard(text){jlog('showCard');latestOutput=text||'';outputBody.innerHTML=renderContent(latestOutput);setPanel('outputPanel');sizeTo('output')}
function copyOutput(){if(!latestOutput)return;navigator.clipboard.writeText(latestOutput).then(()=>toast('Copied'))}
function showConfirm(text){confirmTitle.textContent='Review action request';confirmBody.innerHTML=renderText(String(text));setPanel('confirmPanel');sizeTo('confirm')}
function showConfirmCard(cardJSON){confirmTitle.textContent='Review action request';let html=renderText(String(cardJSON));try{const p=JSON.parse(cardJSON);if(p.title)confirmTitle.textContent=p.title;const steps=Array.isArray(p.fields)?p.fields:(p.plan&&Array.isArray(p.plan.steps)?p.plan.steps:null);const parts=[];if(p.plan&&p.plan.goal)parts.push('<div style="margin-bottom:14px;color:var(--ink-2);font-size:13px">'+esc(String(p.plan.goal))+'</div>');if(steps&&steps.length)parts.push(steps.map(f=>'<div class="item" style="margin-bottom:10px"><div class="eyebrow" style="margin-bottom:6px">'+esc(f.label||'Step')+'</div><div>'+esc(String(f.value||''))+'</div></div>').join(''));if(parts.length)html=parts.join('');else if(p&&typeof p==='object')html=renderText(JSON.stringify(p,null,2))}catch(e){}confirmBody.innerHTML=html;setPanel('confirmPanel');sizeTo('confirm')}
function resolveConfirm(ok){window.confirmCallback&&window.confirmCallback(ok);collapseShell()}

/* ─── proactive suggestion card ───────────────────────────────────────────── */
function showSuggestion(id,icon,title,message,action){jlog('showSuggestion '+id+' '+icon);
  const glyph={download:'⭳',calendar:'▦',link:'↗',warn:'△'}[icon]||'•';
  const el=document.getElementById('suggestion');el.dataset.id=id;
  el.innerHTML='<div class="row"><span class="badge'+(icon==='warn'?' warn':'')+'">'+glyph+'</span><div><p class="ttl">'+esc(title)+'</p><p class="sub">'+esc(message)+'</p></div></div>'+
    '<div class="actions right"><button class="btn ghost" onclick="dismissSuggestion()">Dismiss</button>'+(action?'<button class="btn primary" onclick="acceptSuggestion()">'+esc(action)+'</button>':'')+'</div>';
  el.classList.add('shown');refreshIslandVisibility();sizeTo('suggestion');
  clearTimeout(window.__sg);window.__sg=setTimeout(dismissSuggestion,15000);
}
function acceptSuggestion(){const el=document.getElementById('suggestion');if(el&&window.suggestionAccept)window.suggestionAccept(el.dataset.id);hideSuggestion()}
function dismissSuggestion(){const el=document.getElementById('suggestion');if(el&&window.suggestionDismiss)window.suggestionDismiss(el.dataset.id);hideSuggestion()}
function hideSuggestion(){document.getElementById('suggestion').classList.remove('shown');clearTimeout(window.__sg);refreshIslandVisibility();if(!currentPanel&&!dashboard.classList.contains('visible'))sizeTo('pill');publishRegionRects()}

/* ─── settings dashboard ──────────────────────────────────────────────────── */
async function loadSettings(){if(!window.getSettings)return;const s=await window.getSettings();if(!s)return;settingsState=Object.assign(settingsState,s);providerSelect.value=settingsState.llm_provider||'gemini';modelInput.value=settingsState.model||'';apiKeyInput.value=settingsState.api_key||'';baseUrlInput.value=settingsState.base_url||'';voiceToggle.classList.toggle('active',!!settingsState.enable_voice);privacyToggle.classList.toggle('active',!!settingsState.privacy_mode);metricProvider.textContent=(settingsState.llm_provider||'gemini').toUpperCase();overviewProvider.textContent='Model: '+(settingsState.llm_provider||'gemini');overviewVoice.textContent='Voice: '+(settingsState.enable_voice?'On':'Off');overviewPrivacy.textContent='Privacy: '+(settingsState.privacy_mode?'High':'Standard')}
function toggleFlag(f){if(f==='voice'){settingsState.enable_voice=!settingsState.enable_voice;voiceToggle.classList.toggle('active',settingsState.enable_voice);overviewVoice.textContent='Voice: '+(settingsState.enable_voice?'On':'Off')}else{settingsState.privacy_mode=!settingsState.privacy_mode;privacyToggle.classList.toggle('active',settingsState.privacy_mode);overviewPrivacy.textContent='Privacy: '+(settingsState.privacy_mode?'High':'Standard')}}
async function persistSettings(){settingsState.llm_provider=providerSelect.value;settingsState.model=modelInput.value.trim();settingsState.api_key=apiKeyInput.value.trim();settingsState.base_url=baseUrlInput.value.trim();const ok=await window.saveSettings(settingsState.llm_provider,settingsState.api_key,settingsState.model,settingsState.base_url,settingsState.enable_voice,settingsState.privacy_mode);if(ok){metricProvider.textContent=settingsState.llm_provider.toUpperCase();overviewProvider.textContent='Model: '+settingsState.llm_provider;toast('Settings saved')}else toast('Save failed')}
function setConn(prefix,connected,text,pills){const badge=document.getElementById(prefix+'Badge'),txt=document.getElementById(prefix+'StatusText'),link=document.getElementById(prefix+'LinkBtn'),unlink=document.getElementById(prefix+'UnlinkBtn'),caps=document.getElementById(prefix+'Capabilities');badge.classList.remove('connected','disconnected');badge.classList.add(connected?'connected':'disconnected');badge.textContent=connected?'Connected':'Disconnected';txt.textContent=text;link.classList.toggle('hidden',connected);unlink.classList.toggle('hidden',!connected);if(caps&&pills&&pills.length)caps.innerHTML=pills.map(x=>'<span class="taglet">'+esc(x)+'</span>').join('')}
async function loadIntegrationStatusesDash(){let total=0;if(window.getGoogleStatus){const g=await window.getGoogleStatus();const on=!!(g&&g.connected);if(on)total++;setConn('google',on,on?'Connected as '+(g.email||'your account')+' — Docs, Sheets, Slides, Drive, Gmail, Calendar.':'Link Gmail, Calendar, Drive, Docs, Sheets, and Slides for a unified workspace assistant.',g&&g.workspace?g.workspace:['Gmail','Calendar','Drive','Docs','Sheets','Slides'])}if(window.getSpotifyStatus){const s=await window.getSpotifyStatus();const on=!!(s&&s.connected);if(on)total++;setConn('spotify',on,on?'Connected as '+(s.display_name||'your account')+(s.product?' ('+s.product+')':''):'Link Spotify for playback control, queueing, recommendations, and AI-curated sessions.',s&&s.capabilities?s.capabilities:['Playback','Queue','Recommendations','AI Curation'])}if(window.getMicrosoftStatus){const m=await window.getMicrosoftStatus();const on=!!(m&&m.connected);if(on)total++;setConn('microsoft',on,on?'Microsoft account linked for Outlook and calendar workflows.':'Outlook and adjacent Microsoft workflows can be connected here.',m&&m.workspace?m.workspace:['Outlook','Calendar','OneDrive'])}metricConnections.textContent=total+' active'}
async function openSettings(tab){jlog('openSettings');await loadSettings();dashboard.classList.add('visible');if(window.setInputActive)window.setInputActive(true);sizeTo('settings');switchTab(tab||'overview');loadIntegrationStatusesDash();if(!integrationTimer)integrationTimer=setInterval(loadIntegrationStatusesDash,4000);refreshIslandVisibility();publishRegionRects()}
function closeSettings(){jlog('closeSettings');dashboard.classList.remove('visible');clearInterval(integrationTimer);integrationTimer=null;refreshIslandVisibility();if(currentPanel){sizeTo(currentPanel==='commandPanel'?'command':currentPanel==='outputPanel'?'output':'confirm')}else{if(window.setInputActive)window.setInputActive(false);sizeTo('pill');updateUI('idle')}publishRegionRects()}
function switchTab(tab){document.querySelectorAll('.nav button').forEach(b=>b.classList.toggle('active',b.dataset.tab===tab));document.querySelectorAll('.tab').forEach(t=>t.classList.remove('active'));document.getElementById('tab-'+tab).classList.add('active');dashKicker.textContent=dashCopy[tab][0];dashTitle.textContent=dashCopy[tab][1];dashSub.textContent=dashCopy[tab][2]}
function submitCurrentCommand(){const v=commandInput.value.trim();if(!v)return;jlog('submit "'+v+'"');window.submitCommand&&window.submitCommand(v);commandInput.value='';collapseShell();updateUI('thinking','Dispatching…')}
commandInput.addEventListener('keydown',async e=>{if(e.key==='Enter'&&!e.shiftKey){e.preventDefault();submitCurrentCommand();return}if(e.key==='ArrowUp'&&e.altKey&&window.getPrevCommand){e.preventDefault();const p=await window.getPrevCommand();if(p)e.target.value=p}if(e.key==='ArrowDown'&&e.altKey&&window.getNextCommand){e.preventDefault();e.target.value=await window.getNextCommand()}if(e.key==='Escape'){e.preventDefault();collapseShell()}});
document.addEventListener('keydown',e=>{if(e.key==='Escape'&&dashboard.classList.contains('visible'))closeSettings()});

/* ─── expose functions referenced from markup `onclick=` attributes (static and
   innerHTML-injected) and functions Go calls via a bare (non-__agent) Eval ─── */
window.handleIslandClick = handleIslandClick;
window.fillSuggestion = fillSuggestion;
window.copyOutput = copyOutput;
window.showCommand = showCommand;
window.resolveConfirm = resolveConfirm;
window.switchTab = switchTab;
window.closeSettings = closeSettings;
window.persistSettings = persistSettings;
window.toggleFlag = toggleFlag;
window.dismissSuggestion = dismissSuggestion;
window.acceptSuggestion = acceptSuggestion;
window.openSettings = openSettings;
// Handlers the Go bridge calls directly (see bridge handlers registered above)
// and one Go calls via a bare, non-__agent Eval (internal/ui/overlay.go).
window.updateUI = updateUI;
window.showCard = showCard;
window.showConfirmCard = showConfirmCard;
window.showSuggestion = showSuggestion;
window.publishRegionRects = publishRegionRects;
window.loadIntegrationStatusesDash = loadIntegrationStatusesDash;

jlog('overlay loaded');loadSettings();updateUI('idle','Ready');
window.uiReady && window.uiReady();
requestAnimationFrame(publishRegionRects);
