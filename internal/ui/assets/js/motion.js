// internal/ui/assets/js/motion.js
import { PRESENCE_SIZES } from './state.js';

const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

// Grow overshoots ~4% and settles; shrink does not. A bouncing retreat reads as
// unstable, while a bouncing arrival reads as physical. Same reason iOS uses
// asymmetric curves here.
const GROW   = { dur: 460, ease: 'cubic-bezier(.22,1.16,.36,1)' };
const SHRINK = { dur: 380, ease: 'cubic-bezier(.36,0,.24,1)' };

export function morphTo(el, presence, onSettled) {
  const to = PRESENCE_SIZES[presence];
  if (!to) return;

  const growing = to.w * to.h > el.offsetWidth * el.offsetHeight;
  const t = reduced ? { dur: 0, ease: 'linear' } : (growing ? GROW : SHRINK);

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
export function swapContent(host, render) {
  if (reduced) { host.innerHTML = ''; host.appendChild(render()); return }

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
  setTimeout(() => {
    incoming.style.transition = 'opacity 200ms linear, transform 200ms linear';
    incoming.style.opacity = '1';
    incoming.style.transform = 'scale(1)';
  }, 90);
}
