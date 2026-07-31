// internal/ui/assets/js/main.js
// Entry point for the overlay page. ES modules do not create globals, so every
// function referenced from a markup `onclick=`/`oninput=`/`onkeydown=` attribute
// (static or built into an innerHTML string) — and every function Go calls
// directly via webview Eval (bare, not through window.__agent.recv) — must be
// assigned to `window` explicitly below.
//
// Task 7: command/result/approve/controlcenter are now their own modules
// under surfaces/. main.js keeps only the store, rerender(), hover wiring,
// bridge handlers, publishRegionRects, and openSurface/closeSurface — every
// surface routes through those two functions and store.surface; resolve()
// (state.js) is the sole authority on island geometry, so no surface module
// calls morphTo or sets a size directly.

import { resolve } from './state.js';
import { morphTo, swapContent, currentSwapTarget } from './motion.js';
import { unionIslandRect } from './geometry.js';
import { registerActivity, updateActivity, endActivity, activeActivities, renderActivity }
  from './activities.js';
import { render as renderCommand } from './surfaces/command.js';
import { render as renderResult } from './surfaces/result.js';
import { render as renderApprove } from './surfaces/approve.js';
import { loadSettings } from './surfaces/controlcenter.js';

/* ─── logging: everything goes to Go (voice-agent.log) ────────────────────── */
export function jlog(m){try{window.jslog&&window.jslog('[js] '+m)}catch(e){}}

// Republishes on show AND hide (fix-round-2 finding): #toast sits below the
// island/sheet region entirely (bottom:16px, well outside e.g. the result
// sheet's y 10-530), so without a fresh publish while it's visible it was
// never actually part of the clickable/paintable window region — toast('Copied')
// produced no visible feedback at all, silently.
export function toast(t){const el=document.getElementById('toast');el.textContent=t;el.style.display='block';publishRegionRects();clearTimeout(toast.tm);toast.tm=setTimeout(()=>{el.style.display='none';publishRegionRects()},2200)}

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
  if(d.id==='command') openSurface('command');
  else if(d.id==='result') openSurface('result', { text: d.text });
  // 'approve' is no longer sent: RequestConfirmationCard/RequestConfirmation
  // (internal/ui/overlay.go) now drive the trust.approval live activity
  // instead, so the island expands inline with Approve/Cancel rather than
  // opening a full sheet. surfaces/approve.js stays in place (reachable via
  // openSurface('approve', ...) / window.showConfirmCard) as inert legacy UI
  // rather than risk dropping a code path some other caller still expects.
});
window.__agent.on('activity:update', d => updateActivity(d.id, d.data, syncAndRerender));
window.__agent.on('activity:end', d => endActivity(d.id, syncAndRerender));

export function syncAndRerender(){ store.activities = activeActivities(); rerender() }

/* Every surface EXCEPT the island itself: the Control Center dashboard,
   which stays a separately positioned/sized element (see controlcenter.css)
   rather than rendering inside islandBody, because resolve() deliberately
   keeps the island compact while it's open — plus the toast, which sits
   well outside the island/sheet area entirely (fix-round-2: it was never
   included here, so it was never actually part of the published region and
   was invisible no matter what toast() itself did). Shared by both the
   settle-phase publish (publishRegionRects) and the widen-phase publish (the
   union rect in rerender) so neither one can drop a surface the other
   accounts for. */
function collectOtherSurfaceRects(){
  const rects=[];
  document.querySelectorAll('#dashboard.visible, #toast').forEach(el=>{
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
export function publishRegionRects(){
  const rects = collectOtherSurfaceRects();
  const ir = island.getBoundingClientRect();
  if(ir.width>0 && ir.height>0) rects.push({x:ir.left,y:ir.top,w:ir.width,h:ir.height});
  jlog('regionRects '+rects.length);
  window.setRegionRects && window.setRegionRects(rects);
}

/* ─── island store + render loop ──────────────────────────────────────────── */
const island = document.getElementById('island');
const islandBody = document.getElementById('islandBody');
const capLead = document.getElementById('capLead');
const capTrail = document.getElementById('capTrail');

const store = { surface:null, payload:null, activities:[], agentState:'idle',
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

// Content for the command/result/approve surfaces (rendered inline in the
// island body at presence="sheet") is owned by their own modules, keyed by
// contentId === the surface id, exactly like state.js's resolve() sets it.
const surfaceRenderers = { command: renderCommand, result: renderResult, approve: renderApprove };

// Content beyond 'idle' is otherwise owned by whichever activity is on top
// (see activities.js). Returning an empty div for an id with no renderer
// (or no live entry — e.g. it just ttl'd out between resolve() and render)
// keeps the render loop alive rather than throwing.
function renderContentFor(id, presence){
  if(id === 'idle'){
    const d=document.createElement('div');
    d.innerHTML = '<span class="ttl">Ready</span>';
    return d;
  }
  if(surfaceRenderers[id]) return surfaceRenderers[id](store.payload) || document.createElement('div');
  return renderActivity(id, slotFor(presence)) || document.createElement('div');
}

// Caps are content-driven: each live activity's `leading`/`trailing` render
// slots own #capLead/#capTrail while it's on top. Only 'idle' gets the
// Control Center gear here — BUT see the #island .peek-actions element in
// index.html and its CSS in island.css: it renders the SAME gear, unconditionally,
// whenever presence is 'peek', regardless of which activity (if any) owns
// the caps. Hover always promotes idle/any-activity to peek (state.js), so
// settings stays one gesture away even while e.g. spotify.nowplaying or
// agent.run owns the trailing cap. Without that second entry point, the
// Control Center becomes unreachable by mouse for the entire time an
// activity is live — the exact bug fixed once already in Task 5. Surface ids
// (command/result/approve) have no def in activities.js, so renderActivity
// returns null and both caps go empty — the sheet content fills the space.
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
  // Tracks whether the presence branch below started a morph THIS call. Its
  // settle callback (morphTo's third arg) is what publishes the exact shape
  // once the transition finishes; the tail publish at the bottom of this
  // function must not ALSO run when that's pending — see the widen-phase
  // union comment below (fix-round-2 finding: the unconditional tail publish
  // was overwriting the widen-phase union microseconds after publishing it).
  let morphed = false;
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
       surfaces the settle phase does (the Control Center dashboard), not
       just the island. A bare `setRegionRects([u])` here would silently
       replace the whole region with an island-only rect for the ~380-460ms
       morph duration — visibly clipping an open Control Center, for example,
       if a presence change (e.g. idle->dormant on the 1s tick) fires while
       the island itself is hidden (display:none) behind it.

       Just as critical: nothing below this branch may publish again until
       morphTo's settle callback does, or that settle-phase publish (the
       exact shape) is immediately clobbered right back to a snapshot of
       mid-transition geometry taken microseconds after the transition
       started — which is what `morphed` guards against, below. */
    const rects = collectOtherSurfaceRects();
    const ir = island.getBoundingClientRect();
    const u = unionIslandRect(ir, applied.presence || r.presence, r.presence);
    if(u) rects.push(u); // island rect omitted entirely when it isn't visible
    if(rects.length) window.setRegionRects && window.setRegionRects(rects);
    // ...then narrow it to the exact shape once the morph settles.
    morphTo(island, r.presence, publishRegionRects);
    applied.presence = r.presence;
    morphed = true;
  }
  if(r.contentId !== applied.contentId){
    swapContent(islandBody, () => renderContentFor(r.contentId, r.presence));
    updateCaps(r.contentId);
    // Drives the .peek-actions CSS gate in island.css: that row is the
    // settings gear's second entry point (see index.html), shown whenever
    // presence is 'peek' UNLESS the idle cap already has its own gear.
    island.dataset.content = r.contentId;
    applied.contentId = r.contentId;
  } else if(!surfaceRenderers[r.contentId]){
    // Same content id, but its underlying data (or the compact/expanded
    // slot) may have moved — the step ticker on agent.run, a spotify track
    // change, a nudge's message. swapContent's fade+blur transition exists
    // to sell an OBJECT changing, not a VALUE ticking, so refresh in place
    // instead of re-running the morph animation on every notify event.
    //
    // Gated to non-surface (activity) content only (fix-round-2 finding):
    // command/result/approve build their own DOM once and own it afterward —
    // rebuilding them here on every tick (the 1s idle interval, hover
    // enter/leave) destroyed and re-created the command sheet's <textarea>
    // under the user's cursor, wiping anything typed roughly once a second.
    //
    // Must NOT assume the outgoing swap's target is islandBody.firstElementChild:
    // for ~120ms after a contentId change, swapContent leaves BOTH the
    // fading-out old node and the fading-in new node in islandBody, with the
    // stale one still first. currentSwapTarget() tracks the real one
    // (host.__current, set by swapContent) so an update landing mid-swap
    // patches the node actually on its way in, not the one on its way out —
    // otherwise the real incoming node is orphaned on screen as a permanent
    // absolutely-positioned ghost (fix-round-1 finding).
    const fresh = renderContentFor(r.contentId, r.presence);
    const current = currentSwapTarget(islandBody);
    if(current) islandBody.replaceChild(fresh, current);
    else islandBody.appendChild(fresh);
    islandBody.__current = fresh;
    updateCaps(r.contentId);
  }
  setSurface(r.surface);
  // See the `morphed` comment above: when a morph just started, its own
  // settle callback (morphTo's third arg, always publishRegionRects) is
  // responsible for the next publish, once the transition actually finishes.
  // Publishing here too would immediately overwrite the widen-phase union
  // with a snapshot of mid-transition geometry, defeating the two-phase
  // design entirely.
  if(!morphed) publishRegionRects();
}

// Toggles the Control Center dashboard's visibility in lockstep with
// store.surface. Every other surface (command/result/approve) renders
// inline in islandBody via renderContentFor above, so this is the only
// non-island surface left to drive.
function setSurface(id){
  const dash = document.getElementById('dashboard');
  if(dash) dash.classList.toggle('visible', id === 'controlcenter');
}

// The single entry point for opening any surface (command/result/approve/
// controlcenter). Sets store.surface, stashes the payload the surface's
// render() will consume, and re-runs the render loop — resolve() decides
// the resulting presence and geometry, never this function.
export function openSurface(id, payload){
  store.surface = id;
  store.payload = payload || null;
  if(id === 'command' || id === 'controlcenter'){
    window.setInputActive && window.setInputActive(true);
  }
  rerender();
}

// Lets a surface module (approve.js) tell whether IT is the thing currently
// open, without handing out the store itself — resolveConfirm answers both
// the trust.approval activity (which never touches store.surface) and this
// legacy sheet (which does), and must only close the latter.
export function getSurface(){ return store.surface }

// Clears store.surface/payload, resets the idle clock so dormant timing
// starts fresh from the moment a surface closes, and re-renders.
export function closeSurface(){
  store.surface = null;
  store.payload = null;
  window.setInputActive && window.setInputActive(false);
  store.idleSince = Date.now();
  rerender();
}
window.openSurface = openSurface;
window.closeSurface = closeSurface;

// Trailing control on agent.run — hides the progress display without touching
// the running plan (see activities.js note in Task 6).
window.dismissRunDisplay = () => { endActivity('agent.run', syncAndRerender) };

island.addEventListener('mouseenter', () => { store.hover = true;  rerender() });
island.addEventListener('mouseleave', () => { store.hover = false; rerender() });
// !store.surface (fix-round-2 finding): without this, the command/result/
// approve sheets — which never change agentState or activities — kept
// re-entering rerender() every second while open, which (before the
// surfaceRenderers guard above) rebuilt their DOM out from under the user.
// Kept even now that the guard exists, since there's nothing for this tick
// to do while a surface owns the island's content regardless.
setInterval(() => { if(store.agentState==='idle' && !store.activities.length && !store.surface) rerender() }, 1000);

// Esc closes whichever surface (if any) is open — command, result, approve,
// or the Control Center — uniformly, per Task 7.
document.addEventListener('keydown', e => {
  if(e.key === 'Escape' && store.surface){ e.preventDefault(); closeSurface(); }
});

/* ─── island state driven by Go pushes ────────────────────────────────────── */
function handleIslandClick(){
  // Clicking the island while the Control Center is open is a no-op — only
  // Esc or its own Close button dismiss it, matching the pre-Task-7 dashboard
  // behavior of ignoring island clicks entirely while it was visible.
  if(store.surface === 'controlcenter') return;
  if(store.surface){ closeSurface(); return }
  jlog('island click -> triggerListen');
  window.triggerListen && window.triggerListen();
}
window.handleIslandClick = handleIslandClick;

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
window.updateUI = updateUI;

/* ─── proactive nudge (ambient.nudge activity) accept/dismiss ─────────────
   The nudge itself renders inline in the island (activities.js), not in a
   separate card, so there is no show/hide-card plumbing left here — just
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

jlog('overlay loaded'); loadSettings(); updateUI('idle','Ready');
window.uiReady && window.uiReady();
requestAnimationFrame(publishRegionRects);
