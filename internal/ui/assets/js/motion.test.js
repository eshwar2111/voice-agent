// internal/ui/assets/js/motion.test.js
// swapContent() itself needs a real DOM (it creates/styles/appends actual
// nodes and schedules removal via setTimeout on them) and is NOT covered
// here — that would need a browser or a DOM shim, and this project takes no
// new npm packages, so it stays a hand-verification item (see the fix-round-1
// report). currentSwapTarget() was deliberately split out as pure decision
// logic — it only compares `.parentNode`/`.firstElementChild` — so IT is
// testable with plain objects standing in for nodes, no DOM required.
import test from 'node:test';
import assert from 'node:assert/strict';
import { currentSwapTarget } from './motion.js';

test('prefers the tracked __current node while it is still attached to host', () => {
  const host = {};
  const current = { parentNode: host };
  host.__current = current;
  host.firstElementChild = { parentNode: host }; // some stale/other node
  assert.equal(currentSwapTarget(host), current);
});

test('falls back to firstElementChild when nothing is tracked yet (first render)', () => {
  const host = { firstElementChild: null };
  assert.equal(currentSwapTarget(host), null);
  const only = { parentNode: host };
  host.firstElementChild = only;
  assert.equal(currentSwapTarget(host), only);
});

test('falls back to firstElementChild when the tracked node was detached (e.g. by other code)', () => {
  const host = {};
  const detached = { parentNode: null }; // no longer host's child
  host.__current = detached;
  const real = { parentNode: host };
  host.firstElementChild = real;
  assert.equal(currentSwapTarget(host), real);
});

test('mid-swap: two children present, __current correctly picks the incoming (not outgoing/first) node', () => {
  // Models the exact window motion.js's swapContent leaves open: outgoing is
  // still host.firstElementChild for ~120ms, incoming has already been
  // appended (so is host.__current) but is not first.
  const host = {};
  const outgoing = { parentNode: host };
  const incoming = { parentNode: host };
  host.firstElementChild = outgoing; // stale — would be the WRONG pick
  host.__current = incoming;         // correct — set by swapContent on append
  assert.equal(currentSwapTarget(host), incoming);
  assert.notEqual(currentSwapTarget(host), outgoing);
});
