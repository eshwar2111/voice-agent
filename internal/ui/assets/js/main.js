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

import { resolve, WAKE_MS } from './state.js';
import { morphTo, swapContent, currentSwapTarget } from './motion.js';
import { pairUnionRect, pairLayout } from './geometry.js';
import { registerActivity, updateActivity, endActivity, activeActivities, renderActivity,
  renderProvided, renderForSlot, syncProviderActivities }
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
  //
  // Empty text is overlay.go's ShowNotification 4s auto-clear timer (fix-
  // round-2 finding: this used to just `return`, making that timer a no-op —
  // since agent.run has no ttl, any narration not followed by a real state
  // transition pinned the island to that text forever and blocked dormant).
  //
  // Only end the activity if the agent has actually gone idle by now (fix-
  // round-3 finding: unconditionally ending it here fired even mid-operation
  // — store.agentState still 'acting' — leaving no live entry for
  // renderActivity to find, so the pill fell back to an empty <div> with
  // blank caps for however long remained until the next event. A working
  // agent should never go visually blank.) Otherwise keep the activity alive
  // with an empty text so defaultLabelFor's generic phase label (e.g.
  // "Working…") renders instead of the last, now-stale, narration line —
  // still doesn't force agentState back to idle, so it can't fight a real
  // transition the way the old updateUI('idle', ...) call here once did.
  if(!d.text){
    if(store.agentState === 'idle') endActivity('agent.run', syncAndRerender);
    else updateActivity('agent.run', { phase: store.agentState, text: '' }, syncAndRerender);
    return;
  }
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
// Fires rerender() once, WAKE_MS after the most recent wake, so the island
// actually recedes from 'peek' when wakeUntil expires (I3, whole-branch
// review). The 1s idle tick (setInterval below) is explicitly gated OFF
// whenever any activity is live — exactly the situation a wake happens in —
// so without this, nothing re-ran resolve() until the NEXT unrelated publish;
// with only a meeting live (60s cadence), the island sat at peek for up to a
// minute independent of the I2 latching bug. A one-shot timer (cleared and
// reinstalled on every new wake) avoids running a permanent 1Hz tick for the
// entire time any activity is live, which gating the setInterval condition
// instead would have required.
let wakeTimer = null;
function scheduleWakeExpiry(){
  clearTimeout(wakeTimer);
  wakeTimer = setTimeout(rerender, WAKE_MS);
}

// Provider-driven snapshot (island.Registry -> ui.PublishActivities). Replaces
// the separate `provided` store in activities.js only — never touches `live`
// (trust.approval/agent.run/ambient.nudge), so a snapshot can't clear a
// pending approval. A NEWLY significant id in the snapshot wakes the island
// out of dormant, same as a push-driven significant update would (I2,
// whole-branch review: `newlySignificant` is an edge, unlike the old
// hasSignificantUpdate() scan, which re-fired on every republish of an
// already-significant activity — e.g. a concurrent 1Hz timer emit
// republishing a meeting still marked significant from its own last poll up
// to 60s ago — and kept resetting store.wakeUntil every tick, holding the
// island at peek for up to a minute instead of WAKE_MS).
window.__agent.on('activity:sync', d => {
  syncProviderActivities(d.activities, (newlySignificant) => {
    if(newlySignificant.length){
      store.wakeUntil = Date.now() + WAKE_MS;
      scheduleWakeExpiry();
    }
    syncAndRerender();
  });
});

// syncAndRerender is the onChange callback for EVERY path that can end an
// activity — endActivity(...) call sites (agent.run, ambient.nudge,
// dismissActivity) and the provider-driven activity:sync handler below
// (syncProviderActivities REPLACES the whole `provided` set, so a timer that
// simply stopped being sent disappears here too, with no explicit
// endActivity call at all). Centralizing the store.promoted cleanup here,
// rather than at each call site, is what closes the id-reuse hole (fix-
// round-2 finding): timer ids are caller-supplied (internal/island/timers.go
// prefixes with "timer." but the rest is caller text), so a NEW activity can
// reuse an id that was previously promoted and then ended. resolve() itself
// must stay pure and never clears a stale promoted id (fix-round-1) — this
// is the mutation site fix-round-1's own comment pointed at instead.
export function syncAndRerender(){
  store.activities = activeActivities();
  if(store.promoted && !store.activities.some(a => a.id === store.promoted)){
    store.promoted = null;
  }
  rerender();
}

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
  document.querySelectorAll('#dashboard.visible, #toast, #bubble.shown').forEach(el=>{
    const cs=getComputedStyle(el);
    if(cs.display==='none') return;
    const r=el.getBoundingClientRect();
    if(r.width>0 && r.height>0) rects.push({x:r.left,y:r.top,w:r.width,h:r.height});
  });
  return rects;
}

// While a morph is running the region must stay at the widened union — any
// publish in that window would snapshot mid-transition geometry and clip the
// island. morphTo's settle callback owns the next publish. Enforced HERE,
// inside publishRegionRects itself, rather than at each call site (fix-round
// 3 finding): this is the third distinct occurrence of this bug class in
// this project, each time at a NEW call site (the bubble's transitionend
// handler, toast()) that had no way to see rerender()'s local `morphed`
// variable and so never inherited the `&& !morphed` guard. A per-call-site
// gate can't work for callbacks that fire asynchronously from outside
// rerender() — making it safe by construction here is what stops a fourth
// call site from repeating it.
let morphInFlight = false;

/* Publish every visible surface to Go, which unions them into the window
   region. Anything not covered by these rects is not part of the window at all,
   so clicks there land on the desktop. Called on visibility changes, and on
   morph start/settle from the island's render loop — never per animation frame. */
export function publishRegionRects(){
  if(morphInFlight) return;
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
const bubble = document.getElementById('bubble');

const store = { surface:null, payload:null, activities:[], agentState:'idle',
                hover:false, idleSince:Date.now(), now:Date.now(), promoted:null };
let applied = { presence:null, contentId:null, payload:undefined, bubbleId:null };

// getCanvasSize (overlay.go) is a webview binding — async, and Go's source of
// truth for the canvas' CSS size. Called once, cached here, rather than on
// every rerender()/pairLayout() call: canvasCSSWidth() below just reads the
// cache. 1200 mirrors canvas.go's default until the real value resolves.
let cachedCanvasWidth = 1200;
function canvasCSSWidth(){ return cachedCanvasWidth }

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
// keeps the render loop alive rather than throwing. renderForSlot (not
// renderActivity directly — I4, whole-branch review) tries the push-driven
// `live` map first, then falls back to the provider-driven `provided` map:
// without the fallback, a timer or meeting that became the island's TOP
// activity (contentId), not just the bubble's second one, rendered as a
// blank pill — renderActivity only ever knows about registerActivity-based
// defs (trust.approval/agent.run/ambient.nudge/spotify.nowplaying), never
// kindRenderers.
function renderContentFor(id, presence){
  if(id === 'idle'){
    const d=document.createElement('div');
    d.innerHTML = '<span class="ttl">Ready</span>';
    return d;
  }
  if(surfaceRenderers[id]) return surfaceRenderers[id](store.payload) || document.createElement('div');
  return renderForSlot(id, slotFor(presence)) || document.createElement('div');
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
    // renderForSlot, not renderActivity directly — see renderContentFor's
    // comment above; this is the same gap, and the one that actually made
    // I4's dismiss button unreachable: renderActivity alone never finds
    // kindRenderers.timer/meeting's new 'trailing' slot.
    capLead.replaceChildren(renderForSlot(id,'leading')  || document.createTextNode(''));
    capTrail.replaceChildren(renderForSlot(id,'trailing') || document.createTextNode(''));
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
       started. Enforced by `morphInFlight` inside publishRegionRects()
       itself (see its definition above) — NOT by a local flag checked at
       each call site, so an async callback outside this function (a toast,
       the bubble's transitionend) can't slip a mid-transition snapshot
       through by simply not knowing a morph is running (fix-round-3
       finding: this was the third distinct occurrence of that bug class,
       each time at a new call site that hadn't inherited an earlier `&&
       !morphed` guard).

       The union itself is derived from pairLayout (I1, whole-branch review),
       not from island.getBoundingClientRect(). A measured rect reflects the
       pill's CURRENT centre — but pairLayout shifts that centre whenever
       hasBubble flips (up to (gap+bubbleSize)/2 = 20-26px), and this branch
       can run in the SAME tick a bubble appears or disappears. Centering the
       widen box on the pre-shift measured centre left a strip of the
       SETTLED pill outside the published region for the whole ~460ms morph
       (morphInFlight suppresses every corrective publish until settle) —
       clicks in that strip fell through to the desktop. pairUnionRect takes
       the from/to hasBubble state explicitly (applied.bubbleId is still the
       PRE-this-render value here, since the bubble block below hasn't run
       yet) and unions both assemblies using the same pairLayout math that
       will actually position the DOM, so there is one source of truth for
       horizontal position instead of two that can disagree. */
    const rects = collectOtherSurfaceRects();
    const u = pairUnionRect(applied.presence || r.presence, !!applied.bubbleId,
                             r.presence, !!r.bubbleId, canvasCSSWidth());
    if(u) rects.push(u); // null only for an unrecognized presence string
    if(rects.length) window.setRegionRects && window.setRegionRects(rects);
    // ...then narrow it to the exact shape once the morph settles. morphTo
    // always schedules its settle callback via setTimeout (even the
    // reduced-motion dur:0 path) and clears/reinstalls that timer per call,
    // so a second morph starting before the first settles still clears
    // morphInFlight exactly once — from whichever morphTo call is the last
    // one standing — never zero times and never stuck true.
    morphInFlight = true;
    morphTo(island, r.presence, () => { morphInFlight = false; publishRegionRects(); });
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
    applied.payload = store.payload;
  } else if(r.contentId !== 'command' &&
            (!surfaceRenderers[r.contentId] || store.payload !== applied.payload)){
    // Same content id, but its underlying data (or the compact/expanded
    // slot) may have moved — the step ticker on agent.run, a spotify track
    // change, a nudge's message, or (fix-round-3 finding) a second
    // openSurface('result', {...}) landing while the result sheet was
    // already open (e.g. a background timer's answer arriving while an
    // earlier answer is still on screen — dispatch.go, executor.go,
    // speak.go, productivity.go all route through here). swapContent's
    // fade+blur transition exists to sell an OBJECT changing, not a VALUE
    // ticking, so refresh in place instead of re-running the morph
    // animation on every notify event.
    //
    // Gated to non-surface (activity) content, OR a surface whose payload
    // reference actually changed (fix-round-2 + fix-round-3): 'command' is
    // hard-excluded no matter what — its DOM holds live typed input that a
    // payload check can't tell apart from "the user typed something", so
    // rebuilding it here — even on a genuine payload change — would still
    // wipe whatever's in the textarea. 'result'/'approve' have no such
    // live-input state, so once fix-round-2 stopped them from rebuilding on
    // every no-op tick (`store.payload !== applied.payload` was always
    // false then, since nothing ever updated `applied.payload`), fix-round-3
    // restores their ability to rebuild when openSurface() actually hands
    // them new data — Copy was copying stale text otherwise, since
    // result.js only assigns `latestOutput` inside render().
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
    applied.payload = store.payload;
  }
  // ── bubble ────────────────────────────────────────────────────────────────
  // The detached second activity. resolve() decides bubbleId; this block only
  // renders/positions it — it never touches the island's own presence/size,
  // so resolve() stays the sole authority on island geometry.
  let bubbleEntered = false;
  if(r.bubbleId !== applied.bubbleId){
    if(r.bubbleId){
      bubble.replaceChildren(renderProvided(r.bubbleId,'bubble') ||
                             renderActivity(r.bubbleId,'leading') ||
                             document.createTextNode(''));
      bubble.classList.remove('leaving'); bubble.classList.add('entering','shown');
      bubbleEntered = true;
    } else {
      bubble.classList.remove('entering'); bubble.classList.add('leaving');
      bubble.classList.remove('shown');
    }
    applied.bubbleId = r.bubbleId;
  } else if(r.bubbleId){
    // Same activity, new data — refresh in place, no re-entry animation.
    bubble.replaceChildren(renderProvided(r.bubbleId,'bubble') ||
                           document.createTextNode(''));
  }

  // Position the pair. pairLayout centers the ASSEMBLY, not the pill.
  const lay = pairLayout(r.presence, !!r.bubbleId, canvasCSSWidth());
  island.style.left = lay.pillLeft + 'px';
  if(r.bubbleId){
    bubble.style.left = lay.bubbleLeft + 'px';
    bubble.style.width = lay.bubbleSize + 'px';
    bubble.style.height = lay.bubbleSize + 'px';
  }
  // Publish immediately on becoming shown, now that its geometry is set —
  // not just via the tail publish below. No `morphed`/`!morphInFlight` check
  // needed here: publishRegionRects() itself no-ops while a morph is running
  // (fix-round-3), so this call is always safe to make, whether or not a
  // presence morph also started this tick. If one did, morphTo's settle
  // callback republishes everything once it finishes, and
  // collectOtherSurfaceRects() queries #bubble.shown, so the bubble is
  // included then regardless.
  if(bubbleEntered) publishRegionRects();

  setSurface(r.surface);
  // publishRegionRects() itself is the single point that decides whether a
  // publish is safe right now (see its definition and `morphInFlight`,
  // above) — this call is unconditional on purpose; it is a no-op for the
  // duration of an in-flight morph and otherwise always correct.
  publishRegionRects();
}

// The bubble animates in/out (island.css .entering/.leaving), so its rect
// changes for the duration of that transition — publish on enter (the
// 'shown' class landing, above) and again here on settle, never per frame,
// same two-touch rule the pill's own morph follows. Also finalizes an exit:
// island.css keeps #bubble.leaving at display:grid for the whole transition
// (display isn't animatable on its own), so once the leave settles here,
// dropping the 'leaving' class is what actually removes the bubble from
// display/layout/the region — genuinely gone, not just transparent. A new
// bubble arriving mid-leave never reaches this handler in that state: the
// entering branch above removes 'leaving' itself first, so an interrupted
// exit can't strand the old bubble half-faded.
bubble.addEventListener('transitionend', () => {
  if(bubble.classList.contains('leaving') && !bubble.classList.contains('shown')){
    bubble.classList.remove('leaving');
  }
  publishRegionRects();
});

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

/* ─── bubble promote/dismiss ────────────────────────────────────────────── */
// Clicking the bubble swaps it into the main pill. Implemented as a priority
// nudge rather than a separate "pinned" concept: resolve() stays the only
// thing that decides slots.
window.promoteBubble = () => {
  if(!applied.bubbleId) return;
  store.promoted = applied.bubbleId;
  rerender();
};

window.dismissActivity = (id) => {
  window.dismissIslandActivity && window.dismissIslandActivity(id);
  endActivity(id, syncAndRerender);
};

jlog('overlay loaded'); loadSettings(); updateUI('idle','Ready');
window.uiReady && window.uiReady();
// getCanvasSize is async (webview binding) — fetched once here rather than
// per-frame; canvasCSSWidth() above just reads the cache pairLayout consumes.
if(window.getCanvasSize){
  window.getCanvasSize().then(s => {
    if(s && s.w){ cachedCanvasWidth = s.w; rerender() }
  }).catch(() => {});
}
requestAnimationFrame(publishRegionRects);
