// internal/ui/assets/js/state.js
// Pure island state resolution. No DOM, no globals — so it can be tested with
// `node --test` and reasoned about without running Windows.

export const PRESENCE_SIZES = {
  dormant:  { w: 168, h: 32,  r: 16, opacity: 0.5 },
  compact:  { w: 260, h: 40,  r: 20, opacity: 1 },
  peek:     { w: 420, h: 52,  r: 26, opacity: 1 },
  expanded: { w: 560, h: 180, r: 28, opacity: 1 },
  sheet:    { w: 720, h: 520, r: 30, opacity: 1 },

  // Size-to-content answer tiers for the result surface. The #1 complaint was
  // that a one-line answer opened the full 720x520 sheet ("opening such a big
  // dialog box for a small thing"). resolve() now picks one of these by
  // classifying the answer text (answerPresence, below): a single short line
  // gets a small pill-card, a paragraph a medium card, and only genuinely long
  // or structured content gets the near-full sheet — which still scrolls, so
  // nothing is ever clipped.
  answerSm: { w: 420, h: 112, r: 24, opacity: 1 },
  answerMd: { w: 540, h: 280, r: 28, opacity: 1 },
  answerLg: { w: 680, h: 480, r: 30, opacity: 1 },

  // Interactive choice surface: a MODEST expansion (question + a vertical list
  // of selectable options), not a giant chat window. Height grows with the
  // number of options; past a handful the list itself scrolls.
  choiceSm: { w: 460, h: 240, r: 28, opacity: 1 },
  choiceMd: { w: 480, h: 320, r: 28, opacity: 1 },
  choiceLg: { w: 500, h: 400, r: 30, opacity: 1 },
};

// ─── Content-driven surface sizing ───────────────────────────────────────────
// These are pure (text/array in, presence-or-tier string out) so they can be
// unit-tested and reasoned about the same way resolve() is, and reused by the
// surface renderers (result.js sets data-tier from answerTier) so the DOM and
// the island geometry always agree on the size class.

// looksStructured recognizes the structured JSON list payloads result.js
// renders specially (calendar/gmail/system) — those always want list room.
function looksStructured(text) {
  if (!text || text[0] !== '{') return false;
  try {
    const p = JSON.parse(text);
    return !!(p && (p.type === 'calendar_list' || p.type === 'gmail_list' || p.type === 'system_status'));
  } catch (e) { return false; }
}

// answerTier classifies a result payload into 'sm' | 'md' | 'lg'.
// A streamed answer starts empty and grows, so it opens at least at 'md' to
// give the incoming text room rather than snapping tiny-then-large.
export function answerTier(payload) {
  const text = ((payload && payload.text) || '').trim();
  const streaming = !!(payload && payload.streaming);
  if (looksStructured(text)) return 'lg';
  const len = text.length;
  const lines = text ? text.split('\n').length : 1;
  // Any Markdown list / heading / code fence reads as "rich" — never a one-line pill.
  const rich = /(^|\n)\s*([-*]|\d+\.)\s/.test(text) || /\n\s*#{1,3}\s/.test(text) || text.indexOf('```') !== -1;
  if (streaming && len < 240) return 'md';
  if (!rich && lines <= 1 && len <= 88) return 'sm';
  if (!rich && lines <= 5 && len <= 360) return 'md';
  return 'lg';
}

const ANSWER_PRESENCE = { sm: 'answerSm', md: 'answerMd', lg: 'answerLg' };
export function answerPresence(payload) { return ANSWER_PRESENCE[answerTier(payload)]; }

// choicePresence sizes the interactive choice surface to the option count.
export function choicePresence(payload) {
  const n = (payload && Array.isArray(payload.options)) ? payload.options.length : 0;
  if (n <= 3) return 'choiceSm';
  if (n <= 5) return 'choiceMd';
  return 'choiceLg';
}

export const DORMANT_AFTER_MS = 6000;

// How long a significant update holds the island at peek before it recedes.
export const WAKE_MS = 2500;

// Highest priority wins; ties resolve to whichever registered first, so a
// steady stream of same-priority updates can't make the island flicker.
export function topActivity(activities) {
  if (!activities || !activities.length) return null;
  let best = activities[0];
  for (const a of activities) if (a.priority > best.priority) best = a;
  return best;
}

// Priority-sorted (descending) activity list, ties broken by registration
// order — the same rule topActivity uses, just carried across the whole
// list so the second entry can be found for the bubble slot.
function rankActivities(activities) {
  if (!activities || !activities.length) return [];
  return activities
    .map((a, i) => [a, i])
    .sort((x, y) => (y[0].priority - x[0].priority) || (x[1] - y[1]))
    .map(([a]) => a);
}

// resolve maps the whole store to exactly one
// {presence, contentId, bubbleId, surface}. This is the ONLY function
// allowed to decide the island's size. Everything else mutates the store
// and re-runs it, which is what makes a stray state update unable to snap
// the geometry.
export function resolve(store) {
  const { surface, agentState, hover, idleSince, now } = store;
  let ranked = rankActivities(store.activities);

  // A promoted activity is a priority nudge, not a separate slot concept:
  // resolve() remains the only thing that assigns contentId/bubbleId. If the
  // promoted id still names a live activity, move it to the front of the
  // ranked list before slots are read off. A stale id (idx === -1) is
  // already inert — findIndex fails and this is a no-op on every call — so
  // there is nothing to clear here. resolve() must stay pure: it is the sole
  // authority on island geometry, and a function that mutates its argument
  // on some paths but not others is exactly what later produces "it only
  // misbehaves on the second render." If store.promoted ever needs tidying
  // once its activity ends, that belongs in main.js, where mutation is
  // already expected (alongside the rest of the activity bookkeeping) — not
  // here.
  //
  // C1 (whole-branch review): a live trust.approval must NEVER be reordered
  // out of the top slot by a promotion. Approve/Cancel only exist in the
  // 'expanded' renderer (activities.js) — the 'leading' slot a promoted
  // approval would fall back to in the bubble is just the shield glyph, with
  // no way to resolve it, while RequestConfirmationCard blocks on
  // <-confirmChan indefinitely (overlay.go) and confirmMutex queues any
  // second confirmation behind it. So the approval check below MUST see the
  // true top-priority activity, unaffected by promotion, whether or not
  // trust.approval itself happens to be the promoted id.
  if (store.promoted && !(ranked[0] && ranked[0].id === 'trust.approval')) {
    const idx = ranked.findIndex((a) => a.id === store.promoted);
    if (idx > 0) {
      const [promoted] = ranked.splice(idx, 1);
      ranked = [promoted, ...ranked];
    }
  }

  const bubbleId = ranked[1] ? ranked[1].id : null;

  // 1. User intent outranks everything the agent wants to say.
  if (surface === 'controlcenter') {
    return { presence: 'compact', contentId: 'idle', bubbleId: null, surface: 'controlcenter' };
  }
  if (surface) {
    // The result/answer surface sizes to its content (the #1 complaint); the
    // interactive choice surface expands only modestly. Every other surface
    // (command, approve) keeps the full sheet.
    if (surface === 'result') {
      return { presence: answerPresence(store.payload), contentId: 'result', bubbleId: null, surface: null };
    }
    if (surface === 'askchoice') {
      return { presence: choicePresence(store.payload), contentId: 'askchoice', bubbleId: null, surface: null };
    }
    return { presence: 'sheet', contentId: surface, bubbleId: null, surface: null };
  }

  // 2. Approvals block a plan, so they auto-expand.
  const top = ranked[0] || null;
  if (top && top.id === 'trust.approval') {
    return { presence: 'expanded', contentId: 'trust.approval', bubbleId, surface: null };
  }

  const woken = store.wakeUntil && now < store.wakeUntil;

  // 3. Any other live activity: peek on hover, otherwise compact.
  if (top) {
    // Now-playing is the one activity with a rich hover face: hovering the
    // music pill unfolds the full media widget (its 'expanded' renderer). It
    // stays a quiet `● 🎵` otherwise, so music never occupies the screen.
    if (top.id === 'spotify.nowplaying' && hover) {
      return { presence: 'expanded', contentId: top.id, bubbleId, surface: null };
    }
    // This branch only ever yields 'peek' or 'compact' (never 'dormant'), so
    // the wake check only needs the 'compact' half.
    let presence = hover ? 'peek' : 'compact';
    if (woken && presence === 'compact') presence = 'peek';
    return { presence, contentId: top.id, bubbleId, surface: null };
  }

  // 4. A working agent is never allowed to go dormant.
  if (agentState && agentState !== 'idle') {
    return { presence: hover ? 'peek' : 'compact', contentId: 'agent.run', bubbleId: null, surface: null };
  }

  // 5. Truly idle — shrink out of the way.
  if (hover) return { presence: 'peek', contentId: 'idle', bubbleId: null, surface: null };
  const dormant = (now - idleSince) > DORMANT_AFTER_MS;
  let presence = dormant ? 'dormant' : 'compact';
  if (woken && (presence === 'dormant' || presence === 'compact')) presence = 'peek';
  return { presence, contentId: 'idle', bubbleId: null, surface: null };
}
