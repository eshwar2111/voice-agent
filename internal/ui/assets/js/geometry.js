// internal/ui/assets/js/geometry.js
// Pure region-math helpers for the island's widen-phase region publish. No
// DOM access — callers pass in whatever they measured (e.g. from
// getBoundingClientRect()) — so this is testable under `node --test` and
// kept separate from state.js (which owns *presence* decisions, not region
// geometry).
import { PRESENCE_SIZES } from './state.js';

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

// #island and #bubble both pin `top:10px` in island.css regardless of
// presence/hasBubble — there is no vertical morph — so the widen-phase union
// below only ever needs to reason about the horizontal extent and the
// tallest height in play.
const ASSEMBLY_TOP = 10;

// pairUnionRect returns the bounding box that covers BOTH the from- and
// to-state pill+bubble assemblies, deriving each from pairLayout's own
// horizontal math rather than a measured/centered rect (I1, whole-branch
// review, replacing the former unionIslandRect). unionIslandRect centered its
// box on island.getBoundingClientRect()'s CURRENT centre — but pairLayout
// shifts the pill's centre by (gap+bubbleSize)/2 whenever hasBubble flips,
// so a widen-phase publish computed from the pre-shift measured centre left
// up to 20-26px of the SETTLED pill's left edge outside the published
// region for the entire ~460ms morph (morphInFlight suppresses every
// corrective publish until settle). Deriving both boxes from pairLayout — the
// same function that sets the actual DOM position — keeps one source of
// truth for horizontal position instead of a second, measurement-based one
// that can disagree with it.
//
// Returns null when either presence is unknown. Pure: no DOM.
export function pairUnionRect(fromPresence, fromHasBubble, toPresence, toHasBubble, canvasWidth) {
  const assembly = (presence, hasBubble) => {
    const size = PRESENCE_SIZES[presence];
    if (!size) return null;
    const lay = pairLayout(presence, hasBubble, canvasWidth);
    let left = lay.pillLeft, right = lay.pillLeft + size.w, height = size.h;
    if (hasBubble && lay.bubbleLeft != null) {
      right = Math.max(right, lay.bubbleLeft + lay.bubbleSize);
      height = Math.max(height, lay.bubbleSize);
    }
    return { left, right, height };
  };
  const a = assembly(fromPresence, fromHasBubble), b = assembly(toPresence, toHasBubble);
  if (!a || !b) return null;
  const left = Math.min(a.left, b.left), right = Math.max(a.right, b.right);
  return { x: left, y: ASSEMBLY_TOP, w: right - left, h: Math.max(a.height, b.height) };
}
