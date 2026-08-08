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

// Defined here ahead of Task 7 (bubble/wake scheduling), which is the task
// that actually consumes it beyond the activity:sync handler wired in Task 6.
export const WAKE_MS = 2500;

// Highest priority wins; ties resolve to whichever registered first, so a
// steady stream of same-priority updates can't make the island flicker.
export function topActivity(activities) {
  if (!activities || !activities.length) return null;
  let best = activities[0];
  for (const a of activities) if (a.priority > best.priority) best = a;
  return best;
}

// resolve maps the whole store to exactly one {presence, contentId, surface}.
// This is the ONLY function allowed to decide the island's size. Everything
// else mutates the store and re-runs it, which is what makes a stray state
// update unable to snap the geometry.
export function resolve(store) {
  const { surface, agentState, hover, idleSince, now } = store;

  // 1. User intent outranks everything the agent wants to say.
  if (surface === 'controlcenter') {
    return { presence: 'compact', contentId: 'idle', surface: 'controlcenter' };
  }
  if (surface) {
    return { presence: 'sheet', contentId: surface, surface: null };
  }

  // 2. Approvals block a plan, so they auto-expand.
  const top = topActivity(store.activities);
  if (top && top.id === 'trust.approval') {
    return { presence: 'expanded', contentId: 'trust.approval', surface: null };
  }

  // 3. Any other live activity: peek on hover, otherwise compact.
  if (top) {
    return { presence: hover ? 'peek' : 'compact', contentId: top.id, surface: null };
  }

  // 4. A working agent is never allowed to go dormant.
  if (agentState && agentState !== 'idle') {
    return { presence: hover ? 'peek' : 'compact', contentId: 'agent.run', surface: null };
  }

  // 5. Truly idle — shrink out of the way.
  if (hover) return { presence: 'peek', contentId: 'idle', surface: null };
  const dormant = (now - idleSince) > DORMANT_AFTER_MS;
  return { presence: dormant ? 'dormant' : 'compact', contentId: 'idle', surface: null };
}
