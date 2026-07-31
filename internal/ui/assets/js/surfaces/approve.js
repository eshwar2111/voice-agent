// internal/ui/assets/js/surfaces/approve.js
// The approve sheet. Since SP5 Task 6, RequestConfirmationCard/RequestConfirmation
// (internal/ui/overlay.go) drive the trust.approval LIVE ACTIVITY directly
// (activities.js: 'trust.approval', expanded()) so the island expands inline
// with Approve/Cancel rather than opening this full sheet — no current Go
// caller sends 'surface:open' with id 'approve', and nothing calls
// window.showConfirmCard either. Kept reachable (via
// openSurface('approve', {cardJSON}) or window.showConfirmCard(cardJSON))
// rather than deleted outright, per Task 7's behavior-preservation ruling:
// silently dropping a working code path is worse than carrying dead weight.
// "Reachable" only — it has no committed test coverage and, since nothing
// calls it, has not been exercised end-to-end; treat it as unverified, not
// as a safe fallback.
import { esc } from '../activities.js';
import { endActivity, isLive } from '../activities.js';
import { syncAndRerender, closeSurface, getSurface } from '../main.js';

function renderText(t){
  return '<div>'+esc(t).replace(/\n/g,'<br/>').replace(/\*\*(.*?)\*\*/g,'<strong>$1</strong>')+'</div>';
}

// payload is { cardJSON } (structured plan card) or { msg } (plain text),
// mirroring the pre-Task-7 showConfirmCard/showConfirm split.
export function render(payload){
  let title = 'Review action request';
  let bodyHtml;
  if(payload && payload.cardJSON != null){
    const cardJSON = payload.cardJSON;
    bodyHtml = renderText(String(cardJSON));
    try{
      const p = JSON.parse(cardJSON);
      if(p.title) title = p.title;
      const steps = Array.isArray(p.fields) ? p.fields
        : (p.plan && Array.isArray(p.plan.steps) ? p.plan.steps : null);
      const parts = [];
      if(p.plan && p.plan.goal){
        parts.push('<div style="margin-bottom:14px;color:var(--ink-2);font-size:13px">'+esc(String(p.plan.goal))+'</div>');
      }
      if(steps && steps.length){
        parts.push(steps.map(f=>'<div class="item" style="margin-bottom:10px"><div class="eyebrow" style="margin-bottom:6px">'+esc(f.label||'Step')+'</div><div>'+esc(String(f.value||''))+'</div></div>').join(''));
      }
      if(parts.length) bodyHtml = parts.join('');
      else if(p && typeof p === 'object') bodyHtml = renderText(JSON.stringify(p, null, 2));
    }catch(e){}
  } else {
    bodyHtml = renderText(String((payload && payload.msg) || ''));
  }

  const root = document.createElement('div');
  root.className = 'surface-approve';
  root.innerHTML =
    '<div class="phead"><div><div class="eyebrow">Approval</div><h1>'+esc(title)+'</h1>' +
    '<p>Sensitive actions surface here so you stay in control.</p></div>' +
    '<div class="chip">Safety</div></div>' +
    '<div class="obody">'+bodyHtml+'</div>' +
    '<div class="footer"><div class="actions right">' +
    '<button class="btn ghost" type="button" id="approveCancelBtn">Cancel</button>' +
    '<button class="btn primary" type="button" id="approveOkBtn">Approve</button>' +
    '</div></div>';
  root.querySelector('#approveCancelBtn').onclick = () => resolveConfirm(false);
  root.querySelector('#approveOkBtn').onclick = () => resolveConfirm(true);
  return root;
}

// resolveConfirm answers the trust.approval activity's Approve/Cancel
// buttons (activities.js) AND this sheet's own — it has no ttl (see
// activities.js), so this is the ONLY thing — besides dismissal/quit — that
// ever resolves it; ending the activity here, rather than leaving it for the
// next 'state'/'notify' event to clobber, is what keeps a denied/approved
// plan from lingering expanded.
//
// The trust.approval activity path never touches store.surface (it renders
// via activities.js's expanded() slot, not this module's render()), so
// getSurface() there is whatever else happens to be open — never 'approve'.
// Only when THIS sheet is what's open (store.surface === 'approve', set by
// openSurface('approve', ...) / window.showConfirmCard) do we also close it;
// otherwise the sheet used to have no way back to idle from its own buttons
// (fixed round 1 finding — endActivity alone never touched store.surface).
//
// Guarded against a stray second resolve (fix-round-3 finding): overlay.go
// now queues a second concurrent RequestConfirmation(Card) behind a mutex
// (fix-round-2, I2) rather than letting it silently clobber the first's
// prompt — which means a stray extra confirmCallback send (e.g. a
// double-click on Approve) now has a PLAUSIBLE receiver: the next queued
// caller, in the window between it acquiring the lock and its own prompt
// actually rendering. A double-click must never be able to approve a plan
// the user never saw. `activityLive`/`sheetOpen` are read once, at the top,
// before anything is mutated; if NEITHER is true there is nothing left
// open for this click to legitimately answer, so it's dropped without ever
// calling window.confirmCallback — Go never even sees a second send. This
// is airtight against the double-click case specifically because JS is
// single-threaded: the first click's synchronous handler (which calls
// endActivity/closeSurface below) always runs to completion — deleting the
// registry entry / clearing store.surface — before a second click's handler
// can start, even if the two clicks arrive within the same animation frame.
export function resolveConfirm(ok){
  const activityLive = isLive('trust.approval');
  const sheetOpen = getSurface() === 'approve';
  if(!activityLive && !sheetOpen){
    window.jslog && window.jslog('[js] resolveConfirm: ignoring stray click — nothing pending to resolve');
    return;
  }
  window.confirmCallback && window.confirmCallback(ok);
  if(activityLive) endActivity('trust.approval', syncAndRerender);
  if(sheetOpen) closeSurface();
}
window.resolveConfirm = resolveConfirm;
window.showConfirmCard = (cardJSON) => window.openSurface && window.openSurface('approve', { cardJSON });
