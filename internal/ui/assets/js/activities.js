// internal/ui/assets/js/activities.js
// A live activity owns the island's caps and body while it is the highest
// priority thing happening. Widgets in SP6 register through the same shape,
// with a placement instead of a priority.
//
// Pure registry state (defs/live maps, priority sort, ttl bookkeeping) is
// DOM-free and covered by activities.test.js. Only the render*/el() helpers
// below touch the DOM, and only when actually invoked from the render loop.

const defs = new Map();   // id -> definition
const live = new Map();   // id -> { data, since, timer }

// Provider-driven activities arrive as a full snapshot and REPLACE this set.
// They are deliberately separate from `live` (push-driven): a provider snapshot
// must never be able to clear a pending trust.approval.
const provided = new Map(); // id -> { data, priority, kind, significant }

export function registerActivity(def) {
  if (!def || !def.id) return;
  defs.set(def.id, {
    priority: 0, leading: null, trailing: null,
    compact: null, expanded: null, ttl: 0, onDismiss: null, ...def,
  });
}

export function updateActivity(id, data, onChange) {
  const def = defs.get(id);
  if (!def) { window.jslog && window.jslog('[js] unknown activity ' + id); return }
  const prev = live.get(id);
  if (prev && prev.timer) clearTimeout(prev.timer);
  const entry = { data, since: prev ? prev.since : Date.now(), timer: 0 };
  // trust.approval deliberately has no ttl: auto-denying a plan the user is
  // still reading is worse than waiting forever.
  if (def.ttl > 0) entry.timer = setTimeout(() => endActivity(id, onChange), def.ttl);
  live.set(id, entry);
  onChange && onChange();
}

export function endActivity(id, onChange) {
  const e = live.get(id);
  if (e && e.timer) clearTimeout(e.timer);
  live.delete(id);
  onChange && onChange();
}

// Whether `id` currently has a live entry. Exported for resolveConfirm
// (surfaces/approve.js, fix-round-3): the registry entry for 'trust.approval'
// is deleted, synchronously, the instant the FIRST click's endActivity()
// runs — and a second click's handler cannot start executing until the
// first one has fully returned (JS has one thread) — so checking this at the
// top of resolveConfirm is what makes a stray double-click provably unable
// to resolve a different, later prompt.
export function isLive(id) {
  return live.has(id);
}

// Shape consumed by state.js resolve(): [{id, priority}]. Returns the union
// of the push-driven `live` map and the provider-driven `provided` map so a
// long agent.job (push-driven) sorts alongside provider activities (timer,
// meeting) for bubble eligibility — see syncProviderActivities below.
export function activeActivities() {
  const out = [];
  for (const [id] of live) {
    const def = defs.get(id);
    if (def) out.push({ id, priority: def.priority });
  }
  for (const [id, v] of provided) {
    out.push({ id, priority: v.priority, kind: v.kind });
  }
  return out;
}

// Tracks which provider ids were significant as of the LAST snapshot, so
// syncProviderActivities can tell a genuinely new significant event (I2,
// whole-branch review) from the same one being re-announced. The registry
// publishes a full snapshot on ANY provider's change, and a threshold-crossing
// Activity stays `significant: true` in that snapshot until its own next poll
// (a meeting: up to 60s) — so a concurrent 1Hz timer emit was re-triggering
// "significant" on every tick, latching the wake far past WAKE_MS.
let prevSignificant = new Set();

// Provider-driven activities arrive as a full snapshot from Go
// (island.Registry -> ui.PublishActivities -> 'activity:sync') and REPLACE
// the entire `provided` set. `live` (push-driven: trust.approval, agent.run,
// ambient.nudge) is untouched, so a provider snapshot can never clear a
// pending approval the user is still looking at.
//
// `onChange` is called with the array of ids that are significant in THIS
// snapshot but were NOT in the previous one — an edge, not the latched state
// `significant` itself represents. main.js wakes the island only when that
// array is non-empty, so a meeting's threshold-cross wakes it once, for
// WAKE_MS, regardless of how many unrelated timer snapshots keep republishing
// the same still-significant meeting in between.
export function syncProviderActivities(list, onChange) {
  const nextSignificant = new Set();
  const newlySignificant = [];
  provided.clear();
  for (const a of (list || [])) {
    if (!a || !a.id) continue;
    const significant = !!a.significant;
    provided.set(a.id, {
      data: a.data || {}, priority: a.priority | 0,
      kind: a.kind || '', significant,
    });
    if (significant) {
      nextSignificant.add(a.id);
      if (!prevSignificant.has(a.id)) newlySignificant.push(a.id);
    }
  }
  prevSignificant = nextSignificant;
  onChange && onChange(newlySignificant);
}

// A dismiss control shared by every provider-driven (kindRenderers) trailing
// slot (I4, whole-branch review — the spec's decision 4, "the user can
// dismiss a running activity," was otherwise unreachable: window.dismissActivity
// existed but nothing rendered a control that called it). Copies the
// agent.run trailing pattern (above): a chevron, not a stop glyph, because
// per spec dismissing HIDES the activity — it does not stop the underlying
// timer/meeting, which keeps running and still fires/starts on schedule.
// stopPropagation keeps the click from also bubbling to the island's own
// onclick (triggerListen) or the bubble's (promoteBubble).
function dismissButton(id) {
  const b = el('button', 'iconbtn', icon('chevron'));
  b.title = 'Dismiss';
  b.onclick = (ev) => { ev.stopPropagation(); window.dismissActivity(id) };
  return b;
}

// kindRenderers is keyed by Activity.Kind (the provider path uses `kind`,
// not `id`, so one renderer serves every timer / every meeting). Slots
// receive (data, id) — renderProvided passes both — so `trailing` can wire a
// dismiss control to the right id without needing it baked into `data`.
export const kindRenderers = {
  timer: {
    bubble:  (d) => el('span', 'ring', ringSVG(d.remaining, d.total)),
    leading: (d) => el('span', 'ring', ringSVG(d.remaining, d.total)),
    trailing: (d, id) => dismissButton(id),
    compact: (d) => el('div', null,
      `<span class="ttl">${mmss(d.remaining)}</span>` +
      `<span class="sub">${esc(d.label || 'Timer')}</span>`),
    // Receives (data, id) from renderProvided — id is the ACTIVITY id
    // ("timer.<key>"); the Go bindings strip the "timer." prefix before hitting
    // the store. Pause/Resume freezes/restarts the countdown (the store keeps
    // draining otherwise); Cancel removes the timer outright (spec: Cancel ==
    // Remove). stopPropagation keeps a button click from also bubbling to the
    // island's own onclick (triggerListen).
    expanded: (d, id) => {
      const n = el('div', null,
        `<span class="ttl">${esc(d.label || 'Timer')}</span>` +
        `<span class="sub">${d.paused ? 'Paused · ' : ''}${mmss(d.remaining)} remaining</span>`);
      const row = el('div', 'actions right');
      const toggle = el('button', 'btn ghost', d.paused ? 'Resume' : 'Pause');
      toggle.onclick = (ev) => {
        ev.stopPropagation();
        const fn = d.paused ? window.timerResume : window.timerPause;
        fn && fn(id);
      };
      const cancel = el('button', 'btn ghost', 'Cancel');
      cancel.onclick = (ev) => { ev.stopPropagation(); window.timerCancel && window.timerCancel(id) };
      row.append(toggle, cancel);
      n.appendChild(row);
      return n;
    },
  },
  meeting: {
    bubble:  () => el('span', null, icon('calendar')),
    leading: () => el('span', null, icon('calendar')),
    trailing: (d, id) => dismissButton(id),
    compact: (d) => el('div', null,
      `<span class="ttl">${esc(d.title || 'Meeting')}</span>` +
      `<span class="sub">in ${d.minutes|0}m</span>`),
    expanded: (d) => {
      const n = el('div', null,
        `<span class="ttl">${esc(d.title || 'Meeting')}</span>` +
        `<span class="sub">starts in ${d.minutes|0}m</span>`);
      if(d.joinURL){
        const b = el('button', 'btn primary', 'Join');
        b.onclick = (ev) => { ev.stopPropagation(); window.openExternal &&
                              window.openExternal(d.joinURL) };
        n.appendChild(b);
      }
      return n;
    },
  },
  // Fed by the Go NowPlayingProvider (internal/island/nowplaying.go) through the
  // SAME provider -> provided -> kindRenderers path as timer/meeting, keyed by
  // Kind. (An earlier registerActivity def lived in the push/`live` namespace the
  // provider never reaches, so the pill rendered blank — this is the fix.)
  // Data fields must match what the provider emits: track, artist, art.
  'spotify.nowplaying': {
    bubble:  (d) => albumOrGlyph(d),
    leading: (d) => albumOrGlyph(d),
    trailing: () => el('span', 'eq', '<i></i><i></i><i></i><i></i><i></i>'),
    compact: (d) => el('div', null,
      `<span class="ttl">${esc(d.track || '')} <span class="sub">· ${esc(d.artist || '')}</span></span>`),
    expanded: (d) => el('div', null,
      `<span class="ttl">${esc(d.track || 'Now playing')}</span><span class="sub">${esc(d.artist || '')}</span>`),
  },
};

// Album art when the provider gives it, else the Spotify glyph. Shared by the
// nowplaying leading + bubble slots so the split-island bubble shows the art too.
function albumOrGlyph(d){
  return d.art
    ? el('span', 'art', `<img src="${esc(d.art)}" alt="" width="28" height="28" style="border-radius:6px"/>`)
    : el('span', null, icon('spotify'));
}

function mmss(sec){
  const s = Math.max(0, sec|0);
  return String((s/60)|0).padStart(2,'0') + ':' + String(s%60).padStart(2,'0');
}

// A countdown ring drawn with stroke-dashoffset. r=9 gives circumference ~56.5.
function ringSVG(remaining, total){
  const frac = total > 0 ? Math.max(0, Math.min(1, remaining/total)) : 0;
  const c = 2 * Math.PI * 9;
  return `<svg class="ico" viewBox="0 0 24 24">` +
    `<circle cx="12" cy="12" r="9" stroke="rgba(255,255,255,.18)"/>` +
    `<circle cx="12" cy="12" r="9" stroke="currentColor" ` +
    `stroke-dasharray="${c.toFixed(1)}" stroke-dashoffset="${(c*(1-frac)).toFixed(1)}" ` +
    `transform="rotate(-90 12 12)"/></svg>`;
}

// agent.run progress helpers. Real step data (step/total/label, pushed by
// ui.SetAgentProgress) renders a determinate ring + "N of M · label"; its
// absence falls back to the SP5 indeterminate glyph + phase text, so a command
// with no structured progress is never shown a broken 0% ring.
function agentHasSteps(d){ return !!d && (d.total | 0) > 0; }
function agentPrimary(d){
  if(agentHasSteps(d)){
    const head = `${d.step | 0} of ${d.total | 0}`;
    return d.label ? head + ' · ' + d.label : head;
  }
  return (d && d.text) || 'Working…';
}

export function renderProvided(id, slot) {
  const v = provided.get(id);
  if (!v) return null;
  const r = kindRenderers[v.kind];
  return r && r[slot] ? r[slot](v.data, id) : null;
}

export function renderActivity(id, slot) {
  const def = defs.get(id), e = live.get(id);
  if (!def || !e || !def[slot]) return null;
  return def[slot](e.data);
}

// The single point main.js should call for the island's OWN cap/body content
// (as opposed to the bubble, which has its own explicit renderProvided(...,
// 'bubble') || renderActivity(..., 'leading') fallback chain in rerender()).
// Tries push-driven `live`/defs first, then provider-driven `provided`/
// kindRenderers — the two stores use disjoint id namespaces (trust.approval/
// agent.run/ambient.nudge/spotify.nowplaying vs. timer.*/meeting.*), so at
// most one ever answers. Without this fallback (I4, whole-branch review: this
// is what made the fix reachable at all), main.js's renderContentFor()/
// updateCaps() called renderActivity() exclusively — which only knows the
// `live` map — so a timer or meeting that became the island's TOP activity
// (contentId), not just the bubble's second one, rendered as a blank pill:
// no ring/calendar glyph, no title, and — the immediate reason this mattered
// for I4 — no trailing dismiss button, since capTrail is built the same way.
export function renderForSlot(id, slot) {
  return renderActivity(id, slot) || renderProvided(id, slot);
}

/* ─── esc lives here, not on window: activities.js is an ES module and must
   not depend on main.js having assigned a global before it loads. ────────── */
export function esc(t) {
  return String(t == null ? '' : t)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}
function el(tag, cls, html) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (html != null) n.innerHTML = html;
  return n;
}
const icon = (name) => `<svg class="ico"><use href="#i-${name}"/></svg>`;

registerActivity({
  id: 'trust.approval', priority: 100, ttl: 0,
  leading: (d) => el('span', 'risk-' + (d.risk || 'risky'), icon('shield')),
  trailing: () => el('span'),
  compact: (d) => el('div', null, `<span class="ttl">${esc(d.title || 'Approve action?')}</span>`),
  expanded: (d) => {
    const n = el('div', 'approval');
    let head = `<div class="ttl">${esc(d.title || 'Approve action?')}</div>`;
    if(d.goal) head += `<div class="sub">${esc(d.goal)}</div>`;
    n.innerHTML = head;
    // The steps the user is actually authorising — plain-English label + detail,
    // scrollable if long — instead of a raw JSON blob.
    if(Array.isArray(d.steps) && d.steps.length){
      const list = el('div', 'approval-steps');
      d.steps.forEach(s => {
        const risky = /risky/i.test(s.label || '');
        const item = el('div', 'approval-step' + (risky ? ' risky' : ''));
        item.innerHTML =
          `<span class="astep-label">${esc(s.label || 'Step')}</span>` +
          `<span class="astep-value">${esc(s.value || '')}</span>`;
        list.appendChild(item);
      });
      n.appendChild(list);
    }
    const row = el('div', 'actions right');
    const no = el('button', 'btn ghost', 'Cancel');
    const yes = el('button', 'btn primary', 'Approve');
    no.onclick = () => window.resolveConfirm(false);
    yes.onclick = () => window.resolveConfirm(true);
    row.append(no, yes); n.appendChild(row);
    return n;
  },
});

// task.progress — a long/bulk task's progress card (ui.StartProgress in Go).
// Compact: a status line; expanded: title, a determinate bar when counts are
// known, and a Stop that cancels the task (taskStop binding). phase: running |
// done | error. Sits above agent.run so an active task is the primary display.
registerActivity({
  id: 'task.progress', priority: 95, ttl: 0,
  leading: (d) => {
    if (d.phase === 'done') return el('span', 'cap-glyph good', icon('check'));
    if (d.phase === 'error') return el('span', 'cap-glyph warm', icon('warning'));
    if ((d.total | 0) > 0) return el('span', 'cap-glyph accent', ringSVG(d.done | 0, d.total | 0));
    return el('span', 'cap-glyph accent glyph-pulse', icon('sparkle'));
  },
  trailing: (d) => {
    if (d.phase === 'running' && d.cancelable) {
      const s = el('button', 'iconbtn stop', icon('stop'));
      s.title = 'Stop';
      s.onclick = (ev) => { ev.stopPropagation(); window.taskStop && window.taskStop(); };
      return s;
    }
    return el('span');
  },
  compact: (d) => el('div', null,
    `<span class="ttl">${esc(d.note || d.title || 'Working…')}</span>`),
  expanded: (d) => {
    const n = el('div', 'task-prog');
    let html = `<div class="ttl">${esc(d.title || 'Working…')}</div>`;
    if (d.note) html += `<div class="sub">${esc(d.note)}</div>`;
    if ((d.total | 0) > 0) {
      const pct = Math.max(0, Math.min(100, Math.round((d.done | 0) / (d.total | 0) * 100)));
      html += `<div class="prog-bar"><i style="width:${pct}%"></i></div>` +
              `<div class="sub">${d.done | 0} of ${d.total | 0}</div>`;
    }
    n.innerHTML = html;
    if (d.phase === 'running' && d.cancelable) {
      const row = el('div', 'actions right');
      const stop = el('button', 'btn ghost', 'Stop');
      stop.onclick = (ev) => { ev.stopPropagation(); window.taskStop && window.taskStop(); };
      row.appendChild(stop); n.appendChild(row);
    }
    return n;
  },
});

registerActivity({
  id: 'agent.run', priority: 90, ttl: 0,
  // NOT `.orb` — that is a 9px status DOT with its own gradient and glow,
  // designed to BE the indicator, not to contain one. Wrapping a 20px .ico
  // inside it made the SVG overflow the dot in every direction and crowd the
  // label ("Listening…" appearing to sit on top of the mic). `.cap-glyph`
  // sizes to the cap and colours the icon; the pulse rides on it.
  leading: (d) => {
    if (d.phase === 'listening')
      return el('span', 'cap-glyph accent glyph-pulse', icon('mic'));
    if (d.phase === 'speaking')
      return el('span', 'cap-glyph accent', icon('wave'));
    // A REAL determinate ring once step data is present (reuses ringSVG, the
    // same primitive the timer uses; frac = step/total). Without step data,
    // the SP5 warm sparkle — the equalizer/shimmer carries "busy", so this
    // stays indeterminate rather than a misleading empty ring.
    if (agentHasSteps(d))
      return el('span', 'cap-glyph warm', ringSVG(d.step | 0, d.total | 0));
    return el('span', 'cap-glyph warm', icon('sparkle'));
  },
  trailing: (d) => {
    if (d.phase === 'listening') return el('span', 'eq', '<i></i><i></i><i></i><i></i><i></i>');
    // While speaking, offer a real Stop that halts TTS (executor.StopSpeaking
    // via the stopSpeaking binding). This one genuinely stops the action.
    if (d.phase === 'speaking') {
      const s = el('button', 'iconbtn', icon('stop'));
      s.title = 'Stop talking';
      s.onclick = (ev) => { ev.stopPropagation(); window.stopSpeaking && window.stopSpeaking() };
      return s;
    }
    // NOTE: this dismisses the island's progress display; it does NOT abort the
    // running plan. Real cancellation needs an engine-side binding, and this
    // spec is constrained to internal/ui only. Deferred to SP6 — do not fake it
    // by hiding the UI and letting the plan keep running silently, so the glyph
    // is a chevron (collapse), not a stop square.
    const b = el('button', 'iconbtn', icon('chevron'));
    b.title = 'Hide progress';
    b.onclick = (ev) => { ev.stopPropagation(); window.dismissRunDisplay() };
    return b;
  },
  compact: (d) => el('div', null, `<span class="ttl">${esc(agentPrimary(d))}</span>`),
  expanded: (d) => el('div', null,
    `<span class="ttl">${esc(agentPrimary(d))}</span>` +
    `<span class="sub">${esc(d.detail || '')}</span>`),
});

registerActivity({
  id: 'ambient.nudge', priority: 50, ttl: 8000,
  leading: (d) => el('span', null, icon(({ download: 'download', calendar: 'calendar',
    link: 'link', warn: 'shield' })[d.icon] || 'sparkle')),
  trailing: (d) => {
    if (!d.action) return el('span');
    const b = el('button', 'btn primary', esc(d.action));
    // acceptSuggestion currently reads el.dataset.id and takes no argument
    // (index.html:305). It gains an explicit id parameter in this task:
    //   window.acceptSuggestion = (id) => { window.suggestionAccept &&
    //     window.suggestionAccept(id); endActivity('ambient.nudge', syncAndRerender) }
    b.onclick = (ev) => { ev.stopPropagation(); window.acceptSuggestion(d.id) };
    return b;
  },
  compact: (d) => el('div', null, `<span class="ttl">${esc(d.title || '')}</span>`),
  expanded: (d) => el('div', null,
    `<span class="ttl">${esc(d.title || '')}</span><span class="sub">${esc(d.message || '')}</span>`),
});

// NOTE: spotify.nowplaying is rendered via kindRenderers above (the provider
// path), not here — the provider emits by Kind, which never reaches the
// registerActivity/`live` namespace. Do not re-add a def for it here.
