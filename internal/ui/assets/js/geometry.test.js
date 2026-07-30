// internal/ui/assets/js/geometry.test.js
import test from 'node:test';
import assert from 'node:assert/strict';
import { unionIslandRect } from './geometry.js';

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
