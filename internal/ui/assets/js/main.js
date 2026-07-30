// internal/ui/assets/js/main.js
// Entry point for the overlay page. ES modules do not create globals, so every
// function referenced from a markup `onclick=`/`oninput=`/`onkeydown=` attribute
// (static or built into an innerHTML string) — and every function Go calls
// directly via webview Eval (bare, not through window.__agent.recv) — must be
// assigned to `window` explicitly below.

import { resolve } from './state.js';
import { morphTo, swapContent } from './motion.js';
import { unionIslandRect } from './geometry.js';
import { registerActivity, updateActivity, endActivity, activeActivities, renderActivity }
  from './activities.js';

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
window.__agent.on('notify', d => {
  // ShowNotification is the narration channel: orchestrator.go sends
  // "Step 2/5: …", research_tool.go sends "Reading: …". Feed the ticker
  // rather than forcing the agent state back to idle (the old handler called
  // updateUI('idle', ...) here, which fought the real state transitions).
  if(!d.text){ return }
  updateActivity('agent.run', { phase: store.agentState, text: d.text }, syncAndRerender);
});
window.__agent.on('surface:open', d => {
  if(d.id==='command') showCommand();
  else if(d.id==='result') showCard(d.text);
  // 'approve' is no longer sent: RequestConfirmationCard/RequestConfirmation
  // (internal/ui/overlay.go) now drive the trust.approval live activity
  // instead, so the island expands inline with Approve/Cancel rather than
  // opening a full panel. Old showConfirm/showConfirmCard/confirmPanel
  // markup stays in place as inert legacy UI (unreachable, harmless) rather
  // than risk deleting a code path some other caller still expects.
});
window.__agent.on('activity:update', d => updateActivity(d.id, d.data, syncAndRerender));
window.__agent.on('activity:end', d => endActivity(d.id, syncAndRerender));

function syncAndRerender(){ store.activities = activeActivities(); rerender() }

let currentPanel=null,latestOutput='',integrationTimer=null;
let settingsState={llm_provider:'gemini',api_key:'',model:'',base_url:'',enable_voice:false,privacy_mode:false};
const dashCopy={overview:['Overview','Your assistant at a glance.','See readiness, wire in ecosystems, and tune behavior.'],integrations:['Integrations','Connected ecosystems that feel first-class.','Google Workspace and Spotify behave like native assistant surfaces.'],model:['Models','Shape how the assistant thinks.','Point the runtime at the provider and model that fit your setup.'],privacy:['Privacy','Tune interaction and context posture.','Decide how voice and privacy defaults should feel.']};
function esc(t){return String(t).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#39;')}

/* Every surface EXCEPT the island: panels, the suggestion card, the
   dashboard. Shared by both the settle-phase publish (publishRegionRects)
   and the widen-phase publish (the union rect in rerender) so neither one
   can drop a surface the other accounts for. */
function collectOtherSurfaceRects(){
  const rects=[];
  document.querySelectorAll('.panel.active, .card.shown, #dashboard.visible').forEach(el=>{
    const cs=getComputedStyle(el);
    if(cs.display==='none') return;
    const r=el.getBoundingClientRect();
    if(r.width>0 && r.height>0) rects.push({x:r.left,y:r.top,w:r.width,h:r.height});
  });
  return rects;
}

/* Publish every visible surface to Go, which unions them into the window
   region. Anything not covered by these rects is not part of the window at all,
   so clicks there land on the desktop. Called on visibility changes, and on
   morph start/settle from the island's render loop — never per animation frame. */
function publishRegionRects(){
  const rects = collectOtherSurfaceRects();
  const ir = island.getBoundingClientRect();
  if(ir.width>0 && ir.height>0) rects.push({x:ir.left,y:ir.top,w:ir.width,h:ir.height});
  jlog('regionRects '+rects.length);
  window.setRegionRects && window.setRegionRects(rects);
}
// Compatibility shim: old call sites pass a surface name ('pill'/'command'/...)
// but the argument is now unused — every caller just wants a fresh publish.
// Task 7's real surface routing removes this indirection entirely.
function sizeTo(_v){ publishRegionRects(); }
function toast(t){const el=document.getElementById('toast');el.textContent=t;el.style.display='block';clearTimeout(toast.tm);toast.tm=setTimeout(()=>el.style.display='none',2200)}

/* ─── island store + render loop ──────────────────────────────────────────── */
const island = document.getElementById('island');
const islandBody = document.getElementById('islandBody');
const capLead = document.getElementById('capLead');
const capTrail = document.getElementById('capTrail');

const store = { surface:null, activities:[], agentState:'idle',
                hover:false, idleSince:Date.now(), now:Date.now() };
let applied = { presence:null, contentId:null };

function defaultLabelFor(state){
  if(state==='listening') return 'Listening…';
  if(state==='thinking') return 'Thinking…';
  if(state==='acting') return 'Working…';
  if(state==='speaking') return 'Speaking…';
  return 'Working…';
}

function slotFor(presence){
  return presence === 'expanded' || presence === 'sheet' ? 'expanded' : 'compact';
}

// Content beyond 'idle' is entirely owned by whichever activity is on top
// (see activities.js). Returning an empty div for an id with no renderer
// (or no live entry — e.g. it just ttl'd out between resolve() and render)
// keeps the render loop alive rather than throwing.
function renderContentFor(id, presence){
  if(id === 'idle'){
    const d=document.createElement('div');
    d.innerHTML = '<span class="ttl">Ready</span>';
    return d;
  }
  return renderActivity(id, slotFor(presence)) || document.createElement('div');
}

// Caps are content-driven: each live activity's `leading`/`trailing` render
// slots own #capLead/#capTrail while it's on top. Only 'idle' gets the
// Control Center gear here — BUT see the #island .peek-actions element in
// index.html and its CSS below: it renders the SAME gear, unconditionally,
// whenever presence is 'peek', regardless of which activity (if any) owns
// the caps. Hover always promotes idle/any-activity to peek (state.js), so
// settings stays one gesture away even while e.g. spotify.nowplaying or
// agent.run owns the trailing cap. Without that second entry point, the
// Control Center becomes unreachable by mouse for the entire time an
// activity is live — the exact bug fixed once already in Task 5.
function updateCaps(id){
  if(id === 'idle'){
    capLead.innerHTML = '<svg class="ico"><use href="#i-mic"/></svg>';
    // stopPropagation is required: without it the click also bubbles to
    // #island's own onclick and fires handleIslandClick()->triggerListen()
    // at the same time as opening the Control Center.
    capTrail.innerHTML = '<button type="button" class="cap-btn" title="Open Control Center" aria-label="Open Control Center" onclick="event.stopPropagation();window.openSettings(\'overview\')"><svg class="ico"><use href="#i-gear"/></svg></button>';
  } else {
    capLead.replaceChildren(renderActivity(id,'leading')  || document.createTextNode(''));
    capTrail.replaceChildren(renderActivity(id,'trailing') || document.createTextNode(''));
  }
}

export function rerender(){
  store.now = Date.now();
  const r = resolve(store);
  if(r.presence !== applied.presence){
    /* The region must never be smaller than what CSS is painting at any
       instant of a morph, or the island gets visibly clipped mid-animation.
       Rather than animating the region in lockstep (which needs per-frame
       IPC or the easing curve duplicated in Go), publish the BOUNDING BOX of
       the from- and to-shapes for the duration, then the exact shape once it
       settles. Two calls, not sixty.

       Cost: for the ~460ms of a grow, the surplus area is transparent window
       that eats clicks. Bounded, brief, and confined to where the island is
       about to be.

       Critical: this widen-phase publish must include the SAME non-island
       surfaces the settle phase does (panels/card/dashboard), not just the
       island. A bare `setRegionRects([u])` here would silently replace the
       whole region with an island-only rect for the ~380-460ms morph
       duration — visibly clipping an open Control Center, for example, if a
       presence change (e.g. idle->dormant on the 1s tick) fires while the
       island itself is hidden (display:none) behind it. */
    const rects = collectOtherSurfaceRects();
    const ir = island.getBoundingClientRect();
    const u = unionIslandRect(ir, applied.presence || r.presence, r.presence);
    if(u) rects.push(u); // island rect omitted entirely when it isn't visible
    if(rects.length) window.setRegionRects && window.setRegionRects(rects);
    // ...then narrow it to the exact shape once the morph settles.
    morphTo(island, r.presence, publishRegionRects);
    applied.presence = r.presence;
  }
  if(r.contentId !== applied.contentId){
    swapContent(islandBody, () => renderContentFor(r.contentId, r.presence));
    updateCaps(r.contentId);
    // Drives the .peek-actions CSS gate in island.css: that row is the
    // settings gear's second entry point (see index.html), shown whenever
    // presence is 'peek' UNLESS the idle cap already has its own gear.
    island.dataset.content = r.contentId;
    applied.contentId = r.contentId;
  } else {
    // Same content id, but its underlying data (or the compact/expanded
    // slot) may have moved — the step ticker on agent.run, a spotify track
    // change, a nudge's message. swapContent's fade+blur transition exists
    // to sell an OBJECT changing, not a VALUE ticking, so refresh in place
    // instead of re-running the morph animation on every notify event.
    const fresh = renderContentFor(r.contentId, r.presence);
    if(islandBody.firstElementChild) islandBody.replaceChild(fresh, islandBody.firstElementChild);
    else islandBody.appendChild(fresh);
    updateCaps(r.contentId);
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

// The single entry point for agent state transitions, whether they arrive
// from Go (the 'state' bridge event) or are synthesized locally (e.g.
// submitCurrentCommand's immediate "Dispatching…" before Go's own state
// event lands). Drives the agent.run live activity; rerender() happens as
// that activity's onChange (syncAndRerender), not here directly.
function updateUI(state,text){
  jlog('updateUI '+state+(text?(' "'+text+'"'):''));
  // Reset the dormant clock on every transition INTO idle, so a burst of
  // stray idle notifications can't make the island snap to dormant early or
  // late relative to when the agent actually went quiet.
  const enteringIdle = state === 'idle' && store.agentState !== 'idle';
  store.agentState = state;
  if(enteringIdle) store.idleSince = Date.now();
  if(state === 'idle'){
    endActivity('agent.run', syncAndRerender);
  } else {
    updateActivity('agent.run', { phase: state, text: text || defaultLabelFor(state) },
                    syncAndRerender);
  }
}

function refreshIslandVisibility(){const hide=!!currentPanel||dashboard.classList.contains('visible');island.style.display=hide?'none':'flex';publishRegionRects()}
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
// resolveConfirm answers the trust.approval activity's Approve/Cancel
// buttons (activities.js). It has no ttl (see activities.js), so this is the
// ONLY thing — besides dismissal/quit — that ever resolves it; ending the
// activity here, rather than leaving it for the next 'state'/'notify' event
// to clobber, is what keeps a denied/approved plan from lingering expanded.
function resolveConfirm(ok){window.confirmCallback&&window.confirmCallback(ok);endActivity('trust.approval',syncAndRerender)}

/* ─── proactive nudge (ambient.nudge activity) accept/dismiss ─────────────
   The nudge itself now renders inline in the island (activities.js), not in
   a separate card, so there is no show/hide-card plumbing left here — just
   answering the two buttons the activity's `trailing` slot can produce.
   Auto-dismiss (no user action) is the registry's own 8s ttl. */
window.acceptSuggestion = (id) => {
  window.suggestionAccept && window.suggestionAccept(id);
  endActivity('ambient.nudge', syncAndRerender);
};
window.dismissSuggestion = (id) => {
  window.suggestionDismiss && window.suggestionDismiss(id);
  endActivity('ambient.nudge', syncAndRerender);
};

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
// window.acceptSuggestion / window.dismissSuggestion are assigned directly
// where they're defined, above (they close over the ambient.nudge id).
window.openSettings = openSettings;
// Handlers the Go bridge calls directly (see bridge handlers registered above)
// and one Go calls via a bare, non-__agent Eval (internal/ui/overlay.go).
window.updateUI = updateUI;
window.showCard = showCard;
window.showConfirmCard = showConfirmCard;
window.publishRegionRects = publishRegionRects;
window.loadIntegrationStatusesDash = loadIntegrationStatusesDash;

jlog('overlay loaded');loadSettings();updateUI('idle','Ready');
window.uiReady && window.uiReady();
requestAnimationFrame(publishRegionRects);
