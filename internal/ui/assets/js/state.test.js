// internal/ui/assets/js/state.test.js
import test from 'node:test';
import assert from 'node:assert/strict';
import { resolve, topActivity, PRESENCE_SIZES } from './state.js';

const base = {
  surface: null, activities: [], agentState: 'idle',
  hover: false, idleSince: 0, now: 0,
};
const s = (o) => ({ ...base, ...o });

test('idle collapses to dormant after 6s with cursor away', () => {
  assert.equal(resolve(s({ now: 5999 })).presence, 'compact');
  assert.equal(resolve(s({ now: 6001 })).presence, 'dormant');
});

test('hover always wins over dormant', () => {
  assert.equal(resolve(s({ now: 60000, hover: true })).presence, 'peek');
});

test('open surface outranks every activity', () => {
  const r = resolve(s({
    surface: 'command',
    activities: [{ id: 'trust.approval', priority: 100 }],
  }));
  assert.equal(r.presence, 'sheet');
  assert.equal(r.contentId, 'command');
});

test('control center leaves the island compact', () => {
  const r = resolve(s({ surface: 'controlcenter' }));
  assert.equal(r.presence, 'compact');
  assert.equal(r.surface, 'controlcenter');
});

test('approval auto-expands and outranks a running agent', () => {
  const r = resolve(s({
    agentState: 'acting',
    activities: [
      { id: 'agent.run', priority: 90 },
      { id: 'trust.approval', priority: 100 },
    ],
  }));
  assert.equal(r.presence, 'expanded');
  assert.equal(r.contentId, 'trust.approval');
});

test('now-playing loses to a running agent', () => {
  const r = resolve(s({
    activities: [
      { id: 'spotify.nowplaying', priority: 20 },
      { id: 'agent.run', priority: 90 },
    ],
  }));
  assert.equal(r.contentId, 'agent.run');
});

test('a running agent never sits dormant', () => {
  const r = resolve(s({ now: 999999, agentState: 'listening',
    activities: [{ id: 'agent.run', priority: 90 }] }));
  assert.equal(r.presence, 'compact');
});

test('topActivity picks highest priority, stable for ties', () => {
  assert.equal(topActivity([]), null);
  assert.equal(topActivity([{ id: 'a', priority: 1 }, { id: 'b', priority: 9 }]).id, 'b');
  assert.equal(topActivity([{ id: 'a', priority: 5 }, { id: 'b', priority: 5 }]).id, 'a');
});

test('every presence has a size', () => {
  for (const p of ['dormant', 'compact', 'peek', 'expanded', 'sheet']) {
    assert.ok(PRESENCE_SIZES[p], `${p} has no size`);
    assert.ok(PRESENCE_SIZES[p].w > 0 && PRESENCE_SIZES[p].h > 0);
  }
});

import { WAKE_MS } from './state.js';

const base2 = {
  surface: null, activities: [], agentState: 'idle',
  hover: false, idleSince: 0, now: 0, wakeUntil: 0,
};
const s2 = (o) => ({ ...base2, ...o });

test('second activity goes to the bubble slot', () => {
  const r = resolve(s2({ activities: [
    { id: 'agent.job', priority: 90 },
    { id: 'timer.t1',  priority: 60 },
  ]}));
  assert.equal(r.contentId, 'agent.job');
  assert.equal(r.bubbleId, 'timer.t1');
});

test('a third activity is live but not rendered', () => {
  const r = resolve(s2({ activities: [
    { id: 'a', priority: 90 }, { id: 'b', priority: 60 }, { id: 'c', priority: 10 },
  ]}));
  assert.equal(r.contentId, 'a');
  assert.equal(r.bubbleId, 'b');
  assert.ok(r.bubbleId !== 'c');
});

test('a single activity leaves the bubble empty', () => {
  const r = resolve(s2({ activities: [{ id: 'timer.t1', priority: 60 }] }));
  assert.equal(r.bubbleId, null);
});

test('an approval takes the pill and demotes music to the bubble', () => {
  const r = resolve(s2({ activities: [
    { id: 'spotify.nowplaying', priority: 20 },
    { id: 'trust.approval',     priority: 100 },
  ]}));
  assert.equal(r.contentId, 'trust.approval');
  assert.equal(r.presence, 'expanded');
  assert.equal(r.bubbleId, 'spotify.nowplaying');
});

test('wakeUntil lifts a dormant island to peek', () => {
  const dormant = s2({ now: 60000, idleSince: 0,
                       activities: [{ id: 'timer.t1', priority: 60 }] });
  assert.equal(resolve(dormant).presence, 'compact');

  const woken = { ...dormant, wakeUntil: 60000 + 1000 };
  assert.equal(resolve(woken).presence, 'peek');
});

test('an expired wakeUntil does not hold the island open', () => {
  const r = resolve(s2({ now: 60000, wakeUntil: 59000,
                         activities: [{ id: 'timer.t1', priority: 60 }] }));
  assert.equal(r.presence, 'compact');
});

test('wakeUntil never overrides an open surface', () => {
  const r = resolve(s2({ surface: 'command', now: 1000, wakeUntil: 9999 }));
  assert.equal(r.presence, 'sheet');
  assert.equal(r.contentId, 'command');
});

test('WAKE_MS is a sane wake duration', () => {
  assert.ok(WAKE_MS >= 1500 && WAKE_MS <= 4000);
});
