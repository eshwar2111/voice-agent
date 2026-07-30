// internal/ui/assets/js/activities.test.js
// Registry sorting/priority/ttl logic is DOM-free; only the render*() slots
// touch document.createElement, and this suite never invokes them, so it can
// run under plain `node --test` with no browser shim. The registry's log
// line for an unknown id (`window.jslog && ...`) does read `window`, which
// Node does not define by default, so we stub a minimal one for that case.
import test from 'node:test';
import assert from 'node:assert/strict';
import { registerActivity, updateActivity, endActivity, activeActivities } from './activities.js';
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
