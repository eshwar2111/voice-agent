// internal/ui/assets/js/state.js
// Pure island state resolution. No DOM, no globals — so it can be tested with
// `node --test` and reasoned about without running Windows.

export const PRESENCE_SIZES = {
  dormant:  { w: 168, h: 32,  r: 16, opacity: 0.5 },
  compact:  { w: 260, h: 40,  r: 20, opacity: 1 },
  peek:     { w: 420, h: 52,  r: 26, opacity: 1 },
  expanded: { w: 560, h: 180, r: 28, opacity: 1 },
  sheet:    { w: 720, h: 520, r: 30, opacity: 1 },
};

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
  const ranked = rankActivities(store.activities);
  const bubbleId = ranked[1] ? ranked[1].id : null;

  // 1. User intent outranks everything the agent wants to say.
  if (surface === 'controlcenter') {
    return { presence: 'compact', contentId: 'idle', bubbleId: null, surface: 'controlcenter' };
  }
  if (surface) {
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
    let presence = hover ? 'peek' : 'compact';
    if (woken && (presence === 'dormant' || presence === 'compact')) presence = 'peek';
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
