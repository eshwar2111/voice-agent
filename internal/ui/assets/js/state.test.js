// internal/ui/assets/js/state.test.js
import test from 'node:test';
import assert from 'node:assert/strict';
import { resolve, topActivity, PRESENCE_SIZES, answerTier, answerPresence, choicePresence } from './state.js';

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
  for (const p of ['dormant', 'compact', 'peek', 'expanded', 'sheet',
                   'answerSm', 'answerMd', 'answerLg',
                   'choiceSm', 'choiceMd', 'choiceLg']) {
    assert.ok(PRESENCE_SIZES[p], `${p} has no size`);
    assert.ok(PRESENCE_SIZES[p].w > 0 && PRESENCE_SIZES[p].h > 0);
  }
});

// ─── Size-to-content: the #1 complaint (a one-line answer opened the 720x520 box)
test('a one-line answer is a small pill, not the full sheet', () => {
  assert.equal(answerTier({ text: 'It is 3:42 PM.' }), 'sm');
  const r = resolve(s({ surface: 'result', payload: { text: 'It is 3:42 PM.' } }));
  assert.equal(r.presence, 'answerSm');
  assert.equal(r.contentId, 'result');
  // strictly smaller than the old fixed sheet, in both dimensions
  assert.ok(PRESENCE_SIZES.answerSm.w < PRESENCE_SIZES.sheet.w);
  assert.ok(PRESENCE_SIZES.answerSm.h < PRESENCE_SIZES.sheet.h);
});

test('a short paragraph is medium, a long/rich answer is large', () => {
  assert.equal(answerTier({ text: 'This is a short paragraph that explains one thing in a couple of clauses, then adds a second sentence so it clearly exceeds a single-line pill.' }), 'md');
  const long = Array.from({ length: 40 }, (_, i) => `Line ${i} of a much longer explanation.`).join('\n');
  assert.equal(answerTier({ text: long }), 'lg');
  // a Markdown list is never a one-line pill
  assert.equal(answerTier({ text: '- one\n- two\n- three' }), 'lg');
});

test('a streamed answer opens at least medium so it has room to grow', () => {
  assert.equal(answerTier({ text: '', streaming: true }), 'md');
});

test('structured list payloads get list room', () => {
  assert.equal(answerTier({ text: '{"type":"gmail_list","data":[]}' }), 'lg');
});

test('answerPresence maps tiers to the answer presences', () => {
  assert.equal(answerPresence({ text: 'hi' }), 'answerSm');
  assert.equal(answerPresence({ text: 'a fairly long paragraph '.repeat(6) }), 'answerMd');
});

test('the interactive choice surface expands only modestly, sized to option count', () => {
  const opts2 = { question: 'Which file?', options: [{ id: 'a' }, { id: 'b' }] };
  const r = resolve(s({ surface: 'askchoice', payload: opts2 }));
  assert.equal(r.contentId, 'askchoice');
  assert.equal(r.presence, 'choiceSm');
  // modest: never as tall as the full sheet
  assert.ok(PRESENCE_SIZES.choiceSm.h < PRESENCE_SIZES.sheet.h);
  assert.equal(choicePresence({ options: new Array(4).fill({ id: 'x' }) }), 'choiceMd');
  assert.equal(choicePresence({ options: new Array(8).fill({ id: 'x' }) }), 'choiceLg');
});

test('command and approve surfaces still use the full sheet', () => {
  assert.equal(resolve(s({ surface: 'command' })).presence, 'sheet');
  assert.equal(resolve(s({ surface: 'approve', payload: { msg: 'x' } })).presence, 'sheet');
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

test('wakeUntil lifts a compact island (live activity) to peek', () => {
  const compact = s2({ now: 60000, idleSince: 0,
                       activities: [{ id: 'timer.t1', priority: 60 }] });
  assert.equal(resolve(compact).presence, 'compact');

  const woken = { ...compact, wakeUntil: 60000 + 1000 };
  assert.equal(resolve(woken).presence, 'peek');
});

test('wakeUntil lifts a genuinely dormant island (no activities) to peek', () => {
  const dormant = s2({ now: 60000, idleSince: 0, activities: [] });
  assert.equal(resolve(dormant).presence, 'dormant');

  const woken = { ...dormant, wakeUntil: 60000 + 1000 };
  assert.equal(resolve(woken).presence, 'peek');
});

test('without wakeUntil, an idle island past the threshold stays dormant', () => {
  const r = resolve(s2({ now: 60000, idleSince: 0, activities: [] }));
  assert.equal(r.presence, 'dormant');
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

test('promoting the second activity swaps it into the pill; the previous first becomes the bubble', () => {
  const r = resolve(s2({ promoted: 'timer.t1', activities: [
    { id: 'agent.job', priority: 90 },
    { id: 'timer.t1',  priority: 60 },
  ]}));
  assert.equal(r.contentId, 'timer.t1');
  assert.equal(r.bubbleId, 'agent.job');
});

test('promoting an id that is not live changes nothing, and resolve() leaves the store untouched', () => {
  const store = s2({ promoted: 'ghost', activities: [
    { id: 'agent.job', priority: 90 },
    { id: 'timer.t1',  priority: 60 },
  ]});
  const r = resolve(store);
  assert.equal(r.contentId, 'agent.job');
  assert.equal(r.bubbleId, 'timer.t1');
  // resolve() is the sole authority on island geometry and must stay pure —
  // a stale promoted id is already inert (findIndex fails, so it's a no-op
  // on every subsequent call too) and must not be cleared as a side effect.
  assert.equal(store.promoted, 'ghost');
});

test('promoting the activity already first is a no-op, not a reorder', () => {
  const r = resolve(s2({ promoted: 'agent.job', activities: [
    { id: 'agent.job', priority: 90 },
    { id: 'timer.t1',  priority: 60 },
  ]}));
  assert.equal(r.contentId, 'agent.job');
  assert.equal(r.bubbleId, 'timer.t1');
});

test('C1: a stale promotion cannot demote a live approval out of the top slot', () => {
  const r = resolve(s2({ promoted: 'timer.t1', activities: [
    { id: 'timer.t1',      priority: 60 },
    { id: 'trust.approval', priority: 100 },
  ]}));
  assert.equal(r.presence, 'expanded');
  assert.equal(r.contentId, 'trust.approval');
  assert.equal(r.bubbleId, 'timer.t1');
});

test('now-playing unfolds to the media widget on hover, stays compact otherwise', () => {
  const playing = [{ id: 'spotify.nowplaying', priority: 20 }];
  const hovered = resolve({ activities: playing, hover: true, now: 0, idleSince: 0 });
  assert.equal(hovered.presence, 'expanded');
  assert.equal(hovered.contentId, 'spotify.nowplaying');
  const idle = resolve({ activities: playing, hover: false, now: 0, idleSince: 0 });
  assert.equal(idle.presence, 'compact');
  assert.equal(idle.contentId, 'spotify.nowplaying');
});
