// internal/ui/assets/js/motion.js
import { PRESENCE_SIZES } from './state.js';

// Computed lazily, not at module load: touching `window` at import time made
// this module (and anything pure in it, like currentSwapTarget below)
// impossible to import under `node --test`, which has no `window` global.
// Behavior is unchanged in the browser — matchMedia's result doesn't churn
// mid-session — this just moves the read to call time.
function prefersReducedMotion() {
  return typeof window !== 'undefined' && !!window.matchMedia &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

// Grow overshoots ~4% and settles; shrink does not. A bouncing retreat reads as
// unstable, while a bouncing arrival reads as physical. Same reason iOS uses
// asymmetric curves here.
const GROW   = { dur: 460, ease: 'cubic-bezier(.22,1.16,.36,1)' };
const SHRINK = { dur: 380, ease: 'cubic-bezier(.36,0,.24,1)' };

export function morphTo(el, presence, onSettled) {
  const to = PRESENCE_SIZES[presence];
  if (!to) return;

  const growing = to.w * to.h > el.offsetWidth * el.offsetHeight;
  const t = prefersReducedMotion() ? { dur: 0, ease: 'linear' } : (growing ? GROW : SHRINK);

  // `left` is included here (I5, whole-branch review) even though this
  // function never touches el.style.left itself — main.js's rerender() sets
  // it, synchronously, later in the same tick (pairLayout's pillLeft), and
  // relies on this transition string already being applied to the element
  // for that later assignment to animate rather than teleport. Without it,
  // the bubble arrived over ~380ms while the pill's ~20-26px shift to make
  // room for it (pairLayout re-centers the ASSEMBLY, not the pill alone)
  // happened in a single frame — visibly two uncoordinated movements instead
  // of one. Do this only once I1 (the widen-phase union, geometry.js) is
  // fixed: animating `left` makes the union-rect-vs-settled-position gap
  // span the WHOLE animation instead of resolving after the first frame.
  el.style.transition =
    `width ${t.dur}ms ${t.ease}, height ${t.dur}ms ${t.ease}, ` +
    `border-radius ${t.dur}ms ${t.ease}, left ${t.dur}ms ${t.ease}, opacity 200ms linear`;
  el.style.width = to.w + 'px';
  el.style.height = to.h + 'px';
  el.style.borderRadius = to.r + 'px';
  el.style.opacity = to.opacity;
  el.dataset.presence = presence;

  clearTimeout(el.__morphTimer);
  el.__morphTimer = setTimeout(() => onSettled && onSettled(), t.dur);
}

// Animates ONLY `left` — for the pill's shift when a bubble arrives/departs
// at a CONSTANT presence (whole-branch review, fifth occurrence of the
// region-geometry bug class): width/height/border-radius/opacity don't move
// in that case, so this doesn't touch them, and it overwrites
// el.style.transition outright rather than reusing whatever morphTo last set
// — that string could be stale (a different duration from an unrelated
// presence morph long past), and this needs an exact, known duration to
// schedule its own settle callback correctly. `growing` selects the same
// GROW/SHRINK asymmetry morphTo uses (arrival overshoots, departure doesn't);
// callers pass `true` when a bubble is arriving (the pill making room reads
// as part of that arrival), `false` when one is departing.
export function shiftLeftTo(el, leftPx, growing, onSettled) {
  const t = prefersReducedMotion() ? { dur: 0, ease: 'linear' } : (growing ? GROW : SHRINK);
  el.style.transition = `left ${t.dur}ms ${t.ease}`;
  el.style.left = leftPx + 'px';

  clearTimeout(el.__morphTimer);
  el.__morphTimer = setTimeout(() => onSettled && onSettled(), t.dur);
}

// Content lags the shape: the container reaches its new size BEFORE the new
// content lands. This single detail is what separates a morphing object from a
// box that resizes.
//
// During the ~120ms the outgoing node is fading out, `host` has TWO children
// and `firstElementChild` is the stale outgoing one, not the real content.
// `host.__current` always points at the node an in-place, same-id refresh
// (main.js's rerender()) must target — set here, the instant the incoming
// node is appended, so a same-id activity update that lands mid-swap patches
// the right node instead of splicing into the outgoing node's slot and
// leaving the real (still fading-in) incoming node orphaned on screen.
export function swapContent(host, render) {
  if (prefersReducedMotion()) {
    host.innerHTML = '';
    const only = render();
    host.appendChild(only);
    host.__current = only;
    return;
  }

  // Must use currentSwapTarget(), not host.firstElementChild: when two
  // content changes land within this 120ms fade window, a THIRD/second call
  // would otherwise re-bind outgoing to the FIRST call's already-dying node
  // (still firstElementChild while it fades) instead of the node actually
  // showing right now (host.__current). That leaves the real current node —
  // the previous call's incoming — never faded or removed: a permanent
  // orphaned ghost, absolutely positioned over the real content.
  const outgoing = currentSwapTarget(host);
  if (outgoing) {
    outgoing.style.transition = 'opacity 120ms linear, transform 120ms linear, filter 120ms linear';
    outgoing.style.opacity = '0';
    outgoing.style.transform = 'scale(.96)';
    outgoing.style.filter = 'blur(4px)';
    setTimeout(() => outgoing.remove(), 120);
  }

  const incoming = render();
  incoming.style.opacity = '0';
  incoming.style.transform = 'scale(.96)';
  host.appendChild(incoming);
  host.__current = incoming;
  setTimeout(() => {
    incoming.style.transition = 'opacity 200ms linear, transform 200ms linear';
    incoming.style.opacity = '1';
    incoming.style.transform = 'scale(1)';
  }, 90);
}

// Pure decision logic for "which child does an in-place refresh replace":
// prefer the tracked __current node (kept accurate through an in-flight
// swap by swapContent, above) as long as it's still actually attached to
// host; fall back to firstElementChild only when nothing is tracked (e.g.
// the very first render, before swapContent has ever run). Split out and
// exported so it's testable without a real DOM — it only touches the
// `parentNode`/`firstElementChild` shape, never creates or styles a node.
export function currentSwapTarget(host) {
  if (host.__current && host.__current.parentNode === host) return host.__current;
  return host.firstElementChild || null;
}
