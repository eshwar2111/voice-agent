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

// Provider-driven activities arrive as a full snapshot from Go
// (island.Registry -> ui.PublishActivities -> 'activity:sync') and REPLACE
// the entire `provided` set. `live` (push-driven: trust.approval, agent.run,
// ambient.nudge) is untouched, so a provider snapshot can never clear a
// pending approval the user is still looking at.
export function syncProviderActivities(list, onChange) {
  provided.clear();
  for (const a of (list || [])) {
    if (!a || !a.id) continue;
    provided.set(a.id, {
      data: a.data || {}, priority: a.priority | 0,
      kind: a.kind || '', significant: !!a.significant,
    });
  }
  onChange && onChange();
}

// Any provider activity marked significant in the latest snapshot.
export function hasSignificantUpdate() {
  for (const [, v] of provided) if (v.significant) return true;
  return false;
}

// kindRenderers is populated in Task 9 (timer + meeting slots). Declared
// empty here so this task compiles and runs standalone — renderProvided
// simply returns null until Task 9 fills it in.
export const kindRenderers = {};

export function renderProvided(id, slot) {
  const v = provided.get(id);
  if (!v) return null;
  const r = kindRenderers[v.kind];
  return r && r[slot] ? r[slot](v.data) : null;
}

export function renderActivity(id, slot) {
  const def = defs.get(id), e = live.get(id);
  if (!def || !e || !def[slot]) return null;
  return def[slot](e.data);
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
    const n = el('div', null,
      `<span class="ttl">${esc(d.title || 'Approve action?')}</span>` +
      `<span class="sub">${esc(d.goal || '')}</span>`);
    const row = el('div', 'actions right');
    const no = el('button', 'btn ghost', 'Cancel');
    const yes = el('button', 'btn primary', 'Approve');
    no.onclick = () => window.resolveConfirm(false);
    yes.onclick = () => window.resolveConfirm(true);
    row.append(no, yes); n.appendChild(row);
    return n;
  },
});

registerActivity({
  id: 'agent.run', priority: 90, ttl: 0,
  leading: (d) => el('span', d.phase === 'listening' ? 'orb on pulse' : 'orb on warm', icon(
    d.phase === 'listening' ? 'mic' : 'sparkle')),
  trailing: (d) => {
    if (d.phase === 'listening') return el('span', 'eq', '<i></i><i></i><i></i><i></i><i></i>');
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
  compact: (d) => el('div', null, `<span class="ttl">${esc(d.text || 'Working…')}</span>`),
  expanded: (d) => el('div', null,
    `<span class="ttl">${esc(d.text || 'Working…')}</span>` +
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

registerActivity({
  id: 'spotify.nowplaying', priority: 20, ttl: 0,
  leading: (d) => d.art
    ? el('span', 'art', `<img src="${esc(d.art)}" alt="" width="28" height="28" style="border-radius:6px"/>`)
    : el('span', null, icon('spotify')),
  trailing: () => el('span', 'eq', '<i></i><i></i><i></i><i></i><i></i>'),
  compact: (d) => el('div', null,
    `<span class="ttl">${esc(d.track || '')} <span class="sub">· ${esc(d.artist || '')}</span></span>`),
  expanded: (d) => el('div', null,
    `<span class="ttl">${esc(d.track || '')}</span><span class="sub">${esc(d.artist || '')}</span>`),
});
