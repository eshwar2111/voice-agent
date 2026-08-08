// internal/ui/assets/js/geometry.test.js
import test from 'node:test';
import assert from 'node:assert/strict';
import { pairUnionRect, pairLayout, BUBBLE } from './geometry.js';
import { PRESENCE_SIZES } from './state.js';

test('pairUnionRect covers the larger of two no-bubble presence sizes, centered on the canvas', () => {
  const u = pairUnionRect('compact', false, 'peek', false, 1200);
  assert.equal(u.w, 420); // peek is the wider of the two
  assert.equal(u.h, 52);
  assert.equal(u.y, 10);
  assert.equal(u.x, (1200 - 420) / 2);
});

test('pairUnionRect is order-independent for the max-bounds box', () => {
  const grow = pairUnionRect('compact', false, 'peek', false, 1200);
  const shrink = pairUnionRect('peek', false, 'compact', false, 1200);
  assert.equal(grow.w, shrink.w);
  assert.equal(grow.h, shrink.h);
  assert.equal(grow.x, shrink.x);
});

test('pairUnionRect returns null for an unknown presence', () => {
  assert.equal(pairUnionRect('compact', false, 'bogus', false, 1200), null);
  assert.equal(pairUnionRect('bogus', false, 'compact', false, 1200), null);
});

// I1 (whole-branch review): the widen-phase union used to be centered on
// island.getBoundingClientRect() — the CURRENT measured position — rather
// than derived from pairLayout, so it disagreed with where pairLayout was
// about to place the settled pill+bubble assembly whenever hasBubble flipped
// in the same tick as a presence change. This is the regression test: a
// bubble arriving alongside a dormant->compact morph must publish a widen
// rect that already covers the FULLY SETTLED assembly (pill right edge AND
// bubble right edge), not just the old bubble-less pill's extent.
test('pairUnionRect covers the settled pill+bubble assembly when a bubble arrives mid-morph', () => {
  const canvasWidth = 1200;
  const u = pairUnionRect('dormant', false, 'compact', true, canvasWidth);

  const settled = pairLayout('compact', true, canvasWidth);
  const pillRight = settled.pillLeft + PRESENCE_SIZES.compact.w;
  const bubbleRight = settled.bubbleLeft + settled.bubbleSize;

  assert.ok(u.x <= settled.pillLeft, 'union left edge must not exceed the settled pill');
  assert.ok(u.x + u.w >= bubbleRight, 'union right edge must reach past the settled bubble');
  assert.ok(u.x + u.w >= pillRight);
});

// Scoped re-review finding 1: a bubble arriving/departing at a CONSTANT
// presence also moves the pill's `left` — pairLayout re-centers the whole
// assembly whenever hasBubble flips, regardless of whether presence changes
// alongside it. main.js's rerender() must call pairUnionRect for this case
// too (fromPresence === toPresence, only hasBubble differs), not just for a
// presence change; this pins the geometry math itself is correct for that
// call shape, independent of whether main.js actually makes it (that half
// is verified by inspection/hand-trace, not a test — main.js's DOM shim
// cannot assert on WHICH rects got published, only that nothing throws).
test('pairUnionRect covers both extents when only hasBubble flips (presence constant)', () => {
  const canvasWidth = 1200;
  const u = pairUnionRect('compact', false, 'compact', true, canvasWidth);

  const before = pairLayout('compact', false, canvasWidth);
  const after  = pairLayout('compact', true, canvasWidth);
  const beforeRight = before.pillLeft + PRESENCE_SIZES.compact.w;
  const afterPillRight   = after.pillLeft + PRESENCE_SIZES.compact.w;
  const afterBubbleRight = after.bubbleLeft + after.bubbleSize;

  assert.ok(u.x <= after.pillLeft, 'must not clip the settled pill\'s left edge');
  assert.ok(u.x <= before.pillLeft, 'must not clip the pre-flip pill\'s left edge either');
  assert.ok(u.x + u.w >= afterBubbleRight, 'must reach the settled bubble\'s right edge');
  assert.ok(u.x + u.w >= afterPillRight);
  assert.ok(u.x + u.w >= beforeRight);
  assert.equal(u.h, PRESENCE_SIZES.compact.h); // no bubble is taller than compact
});

test('without a bubble the pill is plain-centered', () => {
  const { pillLeft, bubbleLeft, bubbleSize } = pairLayout('compact', false, 1200);
  assert.equal(pillLeft, (1200 - PRESENCE_SIZES.compact.w) / 2);
  assert.equal(bubbleLeft, null);
  assert.equal(bubbleSize, 0);
});

test('with a bubble the PAIR is centered, not the pill', () => {
  const w = PRESENCE_SIZES.compact.w;
  const total = w + BUBBLE.gap + BUBBLE.compact;
  const { pillLeft, bubbleLeft, bubbleSize } = pairLayout('compact', true, 1200);

  assert.equal(bubbleSize, BUBBLE.compact);
  assert.equal(pillLeft, (1200 - total) / 2);
  assert.equal(bubbleLeft, pillLeft + w + BUBBLE.gap);

  // The assembly's midpoint must be the canvas midpoint.
  assert.equal(pillLeft + total / 2, 600);
});

test('the pill shifts left by exactly half the bubble assembly', () => {
  const without = pairLayout('compact', false, 1200).pillLeft;
  const withB = pairLayout('compact', true, 1200).pillLeft;
  assert.equal(without - withB, (BUBBLE.gap + BUBBLE.compact) / 2);
});

test('the bubble grows at peek', () => {
  assert.equal(pairLayout('peek', true, 1200).bubbleSize, BUBBLE.peek);
});

test('unknown presence falls back to compact rather than throwing', () => {
  const r = pairLayout('nonsense', true, 1200);
  assert.ok(Number.isFinite(r.pillLeft));
  assert.ok(Number.isFinite(r.bubbleLeft));
});
