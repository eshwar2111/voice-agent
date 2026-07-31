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
import { currentSwapTarget, swapContent } from './motion.js';

// Minimal plain-object DOM stand-in, following the same pattern as the
// currentSwapTarget tests above: just enough shape for swapContent to run
// (style bag, parentNode, appendChild, a firstElementChild that reflects
// insertion order, and remove()) — no real DOM/jsdom dependency.
function makeHost() {
  const host = { children: [] };
  Object.defineProperty(host, 'firstElementChild', {
    get() { return host.children[0] || null; },
  });
  host.appendChild = (n) => {
    host.children.push(n);
    n.parentNode = host;
    n.remove = () => {
      const i = host.children.indexOf(n);
      if (i >= 0) host.children.splice(i, 1);
      n.parentNode = null;
    };
  };
  return host;
}
function makeNode(name) {
  return { name, style: {} };
}

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

test('swapContent: two swaps landing within the 120ms fade window do not orphan the second incoming node', async () => {
  const host = makeHost();

  // Settle an initial node (A) with no fade in progress.
  const a = makeNode('A');
  swapContent(host, () => a);
  assert.equal(host.children.length, 1);

  // B swaps in while A is present: A starts its 120ms fade-out, B is
  // appended immediately (fading in) and becomes host.__current.
  const b = makeNode('B');
  swapContent(host, () => b);
  assert.equal(host.__current, b);
  assert.equal(host.children.length, 2); // A (fading out) + B (fading in)

  // C swaps in BEFORE A's 120ms removal timer fires — this is the exact
  // overlapping-swap window the bug lived in. With the bug, outgoing would
  // be host.firstElementChild (still A, the dying node) instead of B, so B
  // would never be faded/removed and would become a permanent ghost.
  const c = makeNode('C');
  swapContent(host, () => c);
  assert.equal(host.__current, c);

  // Give every scheduled fade-out (120ms each) time to run to completion.
  await new Promise((r) => setTimeout(r, 300));

  // Only C should remain: A removed by its own timer, B removed as the
  // outgoing node of the C swap (the fix). Before the fix, B lingered.
  assert.deepEqual(host.children.map((n) => n.name), ['C']);
});
