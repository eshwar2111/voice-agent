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

// Bubble dimensions in CSS px. The bubble is the second visible activity —
// iPhone's detached circle beside the pill.
export const BUBBLE = { compact: 32, peek: 44, gap: 8 };

// pairLayout centers the PILL+BUBBLE ASSEMBLY, not the pill.
//
// This is the whole point: if the pill stayed plain-centered, every activity
// that started would visibly shove the island sideways as the bubble appeared,
// which reads as a bug even though it is deliberate. Instead the pill slides
// left by exactly half the bubble's total width, so the pair's midpoint stays
// on the canvas midpoint and only the bubble appears to arrive.
//
// Pure: takes the canvas width, returns positions. No DOM.
export function pairLayout(presence, hasBubble, canvasWidth) {
  const size = PRESENCE_SIZES[presence] || PRESENCE_SIZES.compact;
  if (!hasBubble) {
    return { pillLeft: (canvasWidth - size.w) / 2, bubbleLeft: null, bubbleSize: 0 };
  }
  const bubbleSize = presence === 'peek' ? BUBBLE.peek : BUBBLE.compact;
  const total = size.w + BUBBLE.gap + bubbleSize;
  const pillLeft = (canvasWidth - total) / 2;
  return { pillLeft, bubbleLeft: pillLeft + size.w + BUBBLE.gap, bubbleSize };
}
