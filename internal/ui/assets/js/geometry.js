// internal/ui/assets/js/geometry.js
// Pure region-math helpers for the island's widen-phase region publish. No
// DOM access — callers pass in whatever they measured (e.g. from
// getBoundingClientRect()) — so this is testable under `node --test` and
// kept separate from state.js (which owns *presence* decisions, not region
// geometry).
import { PRESENCE_SIZES } from './state.js';

// unionIslandRect returns the bounding box that covers BOTH the from- and
// to-presence sizes, centered under the island's currently measured rect.
// Used only for the brief widen-phase publish before a morph settles; the
// settle phase publishes the island's actual (single) rect instead.
//
// Returns null when either presence is unknown, or when `measuredRect` shows
// the island isn't actually visible (zero width/height — e.g. `display:none`
// while the Control Center is open). Callers must not publish an island rect
// in that case: the island isn't part of the visible region at all.
export function unionIslandRect(measuredRect, fromPresence, toPresence) {
  const a = PRESENCE_SIZES[fromPresence], b = PRESENCE_SIZES[toPresence];
  if (!a || !b) return null;
  if (!measuredRect || measuredRect.width <= 0 || measuredRect.height <= 0) return null;
  const cx = measuredRect.left + measuredRect.width / 2, top = measuredRect.top;
  const w = Math.max(a.w, b.w), h = Math.max(a.h, b.h);
  return { x: cx - w / 2, y: top, w, h };
}
