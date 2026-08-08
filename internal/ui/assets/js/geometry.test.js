// internal/ui/assets/js/geometry.test.js
import test from 'node:test';
import assert from 'node:assert/strict';
import { unionIslandRect, pairLayout, BUBBLE } from './geometry.js';
import { PRESENCE_SIZES } from './state.js';

test('unionIslandRect covers the larger of the two presence sizes, centered under the measured rect', () => {
  const measured = { left: 100, top: 10, width: 260, height: 40 }; // compact, on screen
  const u = unionIslandRect(measured, 'compact', 'peek');
  assert.equal(u.w, 420); // peek is the wider of the two
  assert.equal(u.h, 52);
  assert.equal(u.y, 10);
  assert.equal(u.x, 100 + 260 / 2 - 420 / 2);
});

test('unionIslandRect is order-independent for the max-bounds box', () => {
  const measured = { left: 0, top: 0, width: 420, height: 52 };
  const grow = unionIslandRect(measured, 'compact', 'peek');
  const shrink = unionIslandRect(measured, 'peek', 'compact');
  assert.equal(grow.w, shrink.w);
  assert.equal(grow.h, shrink.h);
});

test('unionIslandRect returns null when the island is not actually visible', () => {
  assert.equal(unionIslandRect({ left: 0, top: 0, width: 0, height: 0 }, 'compact', 'peek'), null);
  assert.equal(unionIslandRect(null, 'compact', 'peek'), null);
});

test('unionIslandRect returns null for an unknown presence', () => {
  const measured = { left: 0, top: 0, width: 260, height: 40 };
  assert.equal(unionIslandRect(measured, 'compact', 'bogus'), null);
  assert.equal(unionIslandRect(measured, 'bogus', 'compact'), null);
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
