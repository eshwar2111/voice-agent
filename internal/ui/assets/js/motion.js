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

  el.style.transition =
    `width ${t.dur}ms ${t.ease}, height ${t.dur}ms ${t.ease}, ` +
    `border-radius ${t.dur}ms ${t.ease}, opacity 200ms linear`;
  el.style.width = to.w + 'px';
  el.style.height = to.h + 'px';
  el.style.borderRadius = to.r + 'px';
  el.style.opacity = to.opacity;
  el.dataset.presence = presence;

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

  const outgoing = host.firstElementChild;
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
