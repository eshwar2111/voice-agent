// internal/ui/assets/js/testutil.dom.js
// Minimal DOM shim so main.js — and anything that transitively imports it,
// i.e. every surfaces/*.js module — can be imported under `node --test`,
// which has no browser globals. main.js touches `document`/`window` at
// module top level (event listeners, getElementById, a setInterval), so the
// shim must be in place before main.js is ever imported; import this file
// FIRST (as a side effect) in any test that imports main.js or a surface.
//
// This is deliberately NOT a real DOM: element lookups (getElementById,
// querySelector) return generic, unaffiliated stand-in nodes rather than
// parsing innerHTML — good enough to drive main.js's render loop without
// throwing and to test control flow (does resolveConfirm end up calling
// closeSurface?), but NOT good enough to assert on rendered markup contents.
// Not part of the shipped app — only main.js is linked from index.html.
function makeEl(tag) {
  const el = {
    tagName: tag,
    style: {},
    dataset: {},
    className: '',
    classList: {
      _set: new Set(),
      add(c){ this._set.add(c) },
      remove(c){ this._set.delete(c) },
      toggle(c, v){ if(v===undefined) v = !this._set.has(c); if(v) this._set.add(c); else this._set.delete(c); return v },
      contains(c){ return this._set.has(c) },
    },
    children: [],
    _listeners: {},
    addEventListener(type, fn){ (el._listeners[type] ||= []).push(fn) },
    removeEventListener(){},
    appendChild(c){ el.children.push(c); c.parentNode = el; return c },
    replaceChild(n, o){ const i = el.children.indexOf(o); if(i>=0) el.children[i]=n; else el.children.push(n); n.parentNode = el; o.parentNode = null; return o },
    remove(){ if(el.parentNode){ const i = el.parentNode.children.indexOf(el); if(i>=0) el.parentNode.children.splice(i,1); el.parentNode = null; } },
    replaceChildren(...cs){ el.children = cs },
    querySelector(){ return makeEl('div') },
    querySelectorAll(){ return [] },
    getBoundingClientRect(){ return { left:0, top:0, width:0, height:0 } },
    focus(){},
    get firstElementChild(){ return el.children[0] || null },
    set innerHTML(v){ el._html = v },
    get innerHTML(){ return el._html || '' },
    set textContent(v){ el._text = v },
    get textContent(){ return el._text || '' },
  };
  return el;
}

// Memoized by id (fix-round-2 addition): a regression test needs to observe
// the SAME islandBody the app code holds onto (main.js captures it once, at
// module top level, into a private const) — a fresh stand-in on every call
// would make "is this still the same DOM node across two rerender() calls"
// unanswerable from outside main.js.
const byId = new Map();
function elById(id) {
  if (!byId.has(id)) byId.set(id, makeEl('div'));
  return byId.get(id);
}
// Exposed for tests that need to inspect a specific element by id (e.g.
// document.getElementById('islandBody').__current) without importing
// main.js's private module state.
export function getShimEl(id) { return elById(id) }

globalThis.document = {
  getElementById: elById,
  querySelectorAll(){ return [] },
  addEventListener(){},
  createElement(tag){ return makeEl(tag) },
  createTextNode(t){ return { text: t } },
};
globalThis.window = globalThis;
try { globalThis.navigator = { clipboard: { writeText: async () => {} } }; } catch(e) {}
globalThis.requestAnimationFrame = (fn) => setTimeout(fn, 0);
globalThis.getComputedStyle = () => ({ display: 'block' });

// main.js runs a recurring setInterval at module load (the 1s dormant-tick).
// Left as a normal Node timer it would keep `node --test` from exiting;
// .unref() lets the process end normally once the tests themselves finish,
// without changing the app code's own setInterval call.
const realSetInterval = globalThis.setInterval.bind(globalThis);
globalThis.setInterval = (fn, ms, ...args) => realSetInterval(fn, ms, ...args).unref();
