// internal/ui/assets/js/activities.test.js
// Registry sorting/priority/ttl logic is DOM-free; only the render*() slots
// touch document.createElement, and this suite never invokes them, so it can
// run under plain `node --test` with no browser shim. The registry's log
// line for an unknown id (`window.jslog && ...`) does read `window`, which
// Node does not define by default, so we stub a minimal one for that case.
import test from 'node:test';
import assert from 'node:assert/strict';
import { registerActivity, updateActivity, endActivity, activeActivities, syncProviderActivities, isLive } from './activities.js';
import { topActivity } from './state.js';

test('activeActivities reports the priorities registered for the four v1 activities', () => {
  updateActivity('trust.approval', { title: 'x' });
  updateActivity('agent.run', { text: 'y' });
  updateActivity('spotify.nowplaying', { track: 'z' });
  const active = activeActivities();
  const byId = Object.fromEntries(active.map(a => [a.id, a.priority]));
  assert.equal(byId['trust.approval'], 100);
  assert.equal(byId['agent.run'], 90);
  assert.equal(byId['spotify.nowplaying'], 20);
  endActivity('trust.approval');
  endActivity('agent.run');
  endActivity('spotify.nowplaying');
});

test('topActivity resolves trust.approval over agent.run over ambient.nudge over spotify', () => {
  updateActivity('spotify.nowplaying', {});
  updateActivity('ambient.nudge', {});
  updateActivity('agent.run', {});
  updateActivity('trust.approval', {});
  assert.equal(topActivity(activeActivities()).id, 'trust.approval');
  endActivity('trust.approval');
  assert.equal(topActivity(activeActivities()).id, 'agent.run');
  endActivity('agent.run');
  assert.equal(topActivity(activeActivities()).id, 'ambient.nudge');
  endActivity('ambient.nudge');
  assert.equal(topActivity(activeActivities()).id, 'spotify.nowplaying');
  endActivity('spotify.nowplaying');
});

test('ties resolve to insertion order (state.js topActivity contract), not registry order', () => {
  updateActivity('trust.approval', {});
  registerActivity({ id: 'zzz.tied', priority: 100 });
  updateActivity('zzz.tied', {});
  assert.equal(topActivity(activeActivities()).id, 'trust.approval');
  endActivity('trust.approval');
  endActivity('zzz.tied');
});

test('ambient.nudge auto-expires after its 8s ttl; trust.approval and agent.run never do', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  updateActivity('ambient.nudge', { title: 'x' });
  updateActivity('trust.approval', { title: 'x' });
  updateActivity('agent.run', { text: 'x' });
  t.mock.timers.tick(7999);
  assert.equal(activeActivities().some(a => a.id === 'ambient.nudge'), true);
  t.mock.timers.tick(2);
  assert.equal(activeActivities().some(a => a.id === 'ambient.nudge'), false);
  t.mock.timers.tick(60 * 60 * 1000);
  assert.equal(activeActivities().some(a => a.id === 'trust.approval'), true);
  assert.equal(activeActivities().some(a => a.id === 'agent.run'), true);
  endActivity('trust.approval');
  endActivity('agent.run');
  t.mock.timers.reset();
});

test('refreshing an activity restarts its ttl clock (a live-updating nudge should not vanish mid-refresh)', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  updateActivity('ambient.nudge', { title: 'x' });
  t.mock.timers.tick(7000);
  updateActivity('ambient.nudge', { title: 'x refreshed' }); // clears+restarts the 8s timer
  t.mock.timers.tick(7000);
  assert.equal(activeActivities().some(a => a.id === 'ambient.nudge'), true);
  t.mock.timers.tick(1000);
  assert.equal(activeActivities().some(a => a.id === 'ambient.nudge'), false);
  t.mock.timers.reset();
});

test('registering a definition twice for the same id overwrites rather than duplicates', () => {
  registerActivity({ id: 'spotify.nowplaying', priority: 20 });
  updateActivity('spotify.nowplaying', {});
  const active = activeActivities().filter(a => a.id === 'spotify.nowplaying');
  assert.equal(active.length, 1);
  endActivity('spotify.nowplaying');
});

test('updateActivity on an unregistered id logs and is dropped, never throws into the render loop', () => {
  const prevWindow = globalThis.window;
  const logged = [];
  globalThis.window = { jslog: (m) => logged.push(m) };
  assert.doesNotThrow(() => updateActivity('nonexistent.activity', { x: 1 }));
  assert.equal(activeActivities().some(a => a.id === 'nonexistent.activity'), false);
  assert.equal(logged.length, 1);
  globalThis.window = prevWindow;
});

test('registerActivity ignores a definition with no id', () => {
  assert.doesNotThrow(() => registerActivity({ priority: 5 }));
  assert.doesNotThrow(() => registerActivity(null));
});

// ─── Two-store invariant (SP6): `live` (push, from Go's updateActivity/
// endActivity — trust.approval, agent.run, ambient.nudge) and `provided`
// (provider snapshots via syncProviderActivities — timer.*, meeting.next)
// must stay separate stores. trust.approval has no ttl and blocks the
// executor on a channel waiting for the user's answer; if a provider
// snapshot could ever clear `live`, a routine timer tick would silently
// wipe a pending approval off the screen while the agent stays hung with
// no visible cause. These tests exist to catch a future "simplify to one
// map" refactor that would pass `go build` and every other test.

test('a provider snapshot does not clear a pending trust.approval (two-store invariant)', () => {
  updateActivity('trust.approval', { title: 'Approve action?' });
  syncProviderActivities([
    { id: 'timer.1', priority: 60, kind: 'timer' },
    { id: 'meeting.next', priority: 70, kind: 'meeting' },
  ]);
  assert.equal(isLive('trust.approval'), true,
    'syncProviderActivities (provider-driven, replaces `provided` wholesale) must never ' +
    'touch `live` (push-driven) — trust.approval has no ttl and the executor is blocked ' +
    'waiting on it; if this goes false, the two stores have been merged and a routine ' +
    'provider tick can silently disappear a pending approval while the agent stays hung.');
  assert.equal(
    activeActivities().some(a => a.id === 'trust.approval'), true,
    'trust.approval must still be present in the union returned by activeActivities() ' +
    'after a provider snapshot arrives');
  endActivity('trust.approval');
  syncProviderActivities([]);
});

test('an empty provider snapshot does not clear the push store (the registry legitimately publishes [] when idle)', () => {
  updateActivity('trust.approval', { title: 'Approve action?' });
  syncProviderActivities([]); // exact shape of the real bug: no timers/meetings running
  assert.equal(isLive('trust.approval'), true,
    'an empty provider snapshot is the normal, frequent case (no timers or meetings ' +
    'active) — it must be a no-op for `live`, not an implicit "clear everything"');
  assert.equal(activeActivities().some(a => a.id === 'trust.approval'), true);
  endActivity('trust.approval');
});

test('trust.approval still outranks provider-driven activities in the merged ordering', () => {
  updateActivity('trust.approval', { title: 'Approve action?' }); // priority 100
  syncProviderActivities([
    { id: 'meeting.next', priority: 70, kind: 'meeting' },
    { id: 'timer.1', priority: 60, kind: 'timer' },
  ]);
  assert.equal(topActivity(activeActivities()).id, 'trust.approval',
    'a pending approval (priority 100) must still win topActivity() over provider ' +
    'entries (meeting.next 70, timer.* 60) once the two stores are unioned — losing this ' +
    'ordering would let a meeting or timer bump the approval prompt out of the island');
  endActivity('trust.approval');
  syncProviderActivities([]);
});

test('a provider snapshot fully replaces the previous provider set (provided semantics, pinned alongside the rest)', () => {
  syncProviderActivities([{ id: 'timer.1', priority: 60, kind: 'timer' }]);
  assert.equal(activeActivities().some(a => a.id === 'timer.1'), true);
  syncProviderActivities([{ id: 'meeting.next', priority: 70, kind: 'meeting' }]);
  const active = activeActivities();
  assert.equal(active.some(a => a.id === 'timer.1'), false,
    'a new provider snapshot must wholesale-replace `provided` — timer.1 from the ' +
    'previous snapshot should be gone, not merged with the new list');
  assert.equal(active.some(a => a.id === 'meeting.next'), true);
  syncProviderActivities([]);
});

// I2 (whole-branch review): `significant` in a provider snapshot is a
// LATCHED field — an Activity stays `significant: true` in every republished
// snapshot until its own provider's next poll (a meeting: up to 60s), and the
// registry republishes a FULL snapshot on any provider's change. So a
// concurrent 1Hz timer emit was re-triggering "significant" every tick for up
// to a minute. onChange's `newlySignificant` argument must be an edge — only
// the tick where an id FIRST becomes significant — so main.js's wake-on-
// significant only fires once per actual threshold-cross.
test('syncProviderActivities reports a newly-significant id only on the tick it first becomes significant', () => {
  let seen;
  syncProviderActivities(
    [{ id: 'meeting.next', priority: 70, kind: 'meeting', significant: true }],
    (newlySignificant) => { seen = newlySignificant; });
  assert.deepEqual(seen, ['meeting.next']);

  // A concurrent timer's 1Hz emit republishes the FULL snapshot, including
  // the still-significant meeting from the (up to 60s) call above — this
  // must NOT be reported as newly significant again.
  syncProviderActivities(
    [{ id: 'meeting.next', priority: 70, kind: 'meeting', significant: true },
     { id: 'timer.1',      priority: 60, kind: 'timer',   significant: false }],
    (newlySignificant) => { seen = newlySignificant; });
  assert.deepEqual(seen, []);

  syncProviderActivities([]);
});

test('a re-crossed threshold (significant -> false -> true) is reported as newly significant again', () => {
  let seen;
  syncProviderActivities(
    [{ id: 'meeting.next', priority: 70, kind: 'meeting', significant: true }],
    (n) => { seen = n; });
  assert.deepEqual(seen, ['meeting.next']);

  syncProviderActivities(
    [{ id: 'meeting.next', priority: 70, kind: 'meeting', significant: false }],
    (n) => { seen = n; });
  assert.deepEqual(seen, []);

  syncProviderActivities(
    [{ id: 'meeting.next', priority: 70, kind: 'meeting', significant: true }],
    (n) => { seen = n; });
  assert.deepEqual(seen, ['meeting.next']);

  syncProviderActivities([]);
});
