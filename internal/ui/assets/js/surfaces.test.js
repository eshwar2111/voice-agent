// internal/ui/assets/js/surfaces.test.js
// Smoke coverage for the surfaces/*.js modules split out of main.js in Task
// 7. Real rendered-markup assertions (e.g. "the Approve button's onclick
// really resolves the confirmation") would need a real DOM/browser — this
// project takes no new npm packages, so that stays a hand-verification item,
// same as motion.test.js's swapContent(). What IS testable with the plain
// stand-in DOM in testutil.dom.js: that render() returns a node with the
// expected class without throwing, and — the actual fix-round-1 bug — that
// resolveConfirm() correctly closes the approve SHEET when it's the thing
// open (store.surface === 'approve'), while leaving an unrelated open
// surface alone, exercising the real store/rerender() code path rather than
// a mock of it.
import './testutil.dom.js';
import test from 'node:test';
import assert from 'node:assert/strict';
import { getShimEl } from './testutil.dom.js';
import { openSurface, getSurface, rerender } from './main.js';
import { render as renderCommand } from './surfaces/command.js';
import { render as renderResult } from './surfaces/result.js';
import { render as renderApprove, resolveConfirm } from './surfaces/approve.js';

test('command surface renders a node with the expected class', () => {
  const node = renderCommand();
  assert.equal(node.className, 'surface-command');
});

test('result surface renders a node with the expected class', () => {
  const node = renderResult({ text: 'hello world' });
  assert.equal(node.className, 'surface-result');
});

test('approve surface renders a node with the expected class', () => {
  const node = renderApprove({ msg: 'Approve this?' });
  assert.equal(node.className, 'surface-approve');
});

test('fix round 1: resolveConfirm closes the approve sheet when it is what is open', () => {
  openSurface('approve', { msg: 'test' });
  assert.equal(getSurface(), 'approve');
  resolveConfirm(true);
  assert.equal(getSurface(), null,
    'Approve/Cancel used to resolve the confirmation but never dismiss the sheet itself');
});

test('fix round 1: resolveConfirm leaves an unrelated open surface alone', () => {
  // Models the trust.approval live-activity path, which calls the same
  // resolveConfirm() but never sets store.surface — so if some other
  // surface (here: command) happens to be open at the same time, resolving
  // a trust.approval confirmation must not close it out from under the user.
  openSurface('command');
  assert.equal(getSurface(), 'command');
  resolveConfirm(false);
  assert.equal(getSurface(), 'command');
});

test('fix round 2 (C2): rerender() does not rebuild the command surface root when nothing changed', () => {
  // Before this fix, every rerender() with an unchanged contentId — the 1s
  // idle-tick interval, hover enter/leave on the island itself, which AT
  // sheet size IS the panel being typed into — fell into the in-place
  // "activity value ticked" refresh branch regardless of whether contentId
  // was a live activity or a stateful surface. For 'command' that meant
  // command.js's render() ran again, building a brand-new <textarea> and
  // replacing the old one via replaceChild — wiping anything typed, roughly
  // once a second, and impossible to type a command longer than that.
  openSurface('command');
  const islandBody = getShimEl('islandBody');
  const rootAfterOpen = islandBody.__current;
  assert.ok(rootAfterOpen, 'expected a root node after opening the command surface');

  // Simulates the 1s idle tick / a mouseenter+mouseleave pair: contentId and
  // presence are both still 'command'/'sheet' (state.js's resolve() ignores
  // hover once a surface is open), so nothing SHOULD change.
  rerender();
  rerender();

  assert.equal(islandBody.__current, rootAfterOpen,
    'command sheet was rebuilt in place on a no-op rerender — this destroys whatever the user had typed');
});
