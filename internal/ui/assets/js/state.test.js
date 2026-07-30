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
