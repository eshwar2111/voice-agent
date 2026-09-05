// internal/ui/assets/js/surfaces/askchoice.js
// The interactive CHOICE surface: shown when the agent needs the user to pick
// one option (a clarification, a disambiguation, an approval-with-alternatives)
// rather than type free text. render(payload) is called by main.js when
// store.surface==='askchoice', with payload === { question, options }, where
// each option is { id, label, sub }. Selecting one sends its id back to the
// blocked Go AskChoice via window.askChoiceSubmit(id); cancelling (Esc, click-
// away, or any close) sends window.askChoiceCancel so AskChoice never hangs —
// exactly mirroring ask.js's askTextSubmit/askTextCancel contract.
//
// Options are answerable three ways: click, keyboard (↑/↓ or ←/→ to move,
// Enter to choose, 1-9 to jump), and — via the same askChoiceSubmit binding —
// voice, once the backend routes a spoken answer to it.
import { esc } from '../activities.js';

let opts = [];
let sel = 0;
let rootEl = null;
// Guards against a double submit (native Enter-on-focused-button firing a click
// after the keydown handler already chose). closeSurface()'s follow-up cancel is
// harmless regardless — Go drops a cancel with no waiting receiver (overlay.go),
// same as ask.js — so this only needs to stop a second SUBMIT, not the cancel.
let answered = false;

export function render(payload){
  answered = false;
  const q = (payload && payload.question) || 'Which one?';
  opts = (payload && Array.isArray(payload.options)) ? payload.options : [];
  sel = 0;

  const root = document.createElement('div');
  // surface-command gives the flex-column shell + .phead/.footer styling; the
  // surface-choice modifier adds the option-list styling (surfaces.css).
  root.className = 'surface-command surface-choice';
  rootEl = root;

  const items = opts.map((o, i) =>
    '<button class="choice-opt' + (i === 0 ? ' sel' : '') + '" type="button" data-idx="' + i + '"' +
      ' role="option" aria-selected="' + (i === 0 ? 'true' : 'false') + '">' +
      '<span class="choice-label">' + esc(o.label || o.id || ('Option ' + (i + 1))) + '</span>' +
      (o.sub ? '<span class="choice-sub">' + esc(o.sub) + '</span>' : '') +
    '</button>').join('');

  root.innerHTML =
    '<div class="phead"><div><div class="eyebrow">Your call</div>' +
      '<h1>' + esc(q) + '</h1>' +
      '<p>Pick an option — click, or use ↑ ↓ and Enter.</p></div></div>' +
    '<div class="choice-list" role="listbox">' + items + '</div>' +
    '<div class="footer"><div class="hint"><div>↑ ↓ to move · Enter to choose</div>' +
      '<div><kbd>Esc</kbd> cancel</div></div></div>';

  root.querySelectorAll('.choice-opt').forEach(b => {
    const i = parseInt(b.dataset.idx, 10);
    b.onclick = () => choose(i);
    b.addEventListener('mouseenter', () => setSel(i));
  });
  root.addEventListener('keydown', onKeydown);
  // Focus the selected option so arrow/Enter reach this surface even though the
  // island is normally a no-activate window (main.js sets input active for
  // 'askchoice', so the overlay is foregrounded while the choice is open).
  setTimeout(() => { const el = root.querySelector('.choice-opt.sel'); if (el) el.focus(); }, 80);
  return root;
}

function setSel(i){
  if (i < 0 || i >= opts.length) return;
  sel = i;
  if (!rootEl) return;
  rootEl.querySelectorAll('.choice-opt').forEach((b, idx) => {
    const on = idx === sel;
    b.classList.toggle('sel', on);
    b.setAttribute('aria-selected', on ? 'true' : 'false');
  });
}

function focusSel(){
  if (!rootEl) return;
  const el = rootEl.querySelector('.choice-opt.sel');
  if (el) el.focus();
}

function onKeydown(e){
  if (e.key === 'ArrowDown' || e.key === 'ArrowRight'){
    e.preventDefault(); setSel(Math.min(opts.length - 1, sel + 1)); focusSel();
  } else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft'){
    e.preventDefault(); setSel(Math.max(0, sel - 1)); focusSel();
  } else if (e.key === 'Enter'){
    e.preventDefault(); choose(sel);
  } else if (/^[1-9]$/.test(e.key)){
    const i = parseInt(e.key, 10) - 1;
    if (i < opts.length){ e.preventDefault(); choose(i); }
  }
  // Esc is handled globally in main.js (closeSurface -> askChoiceCancel).
}

function choose(i){
  if (answered) return;
  if (i < 0 || i >= opts.length) return;
  answered = true;
  const o = opts[i];
  const id = (o && o.id != null && o.id !== '') ? String(o.id) : String(i);
  window.askChoiceSubmit && window.askChoiceSubmit(id);
  // closeSurface would also fire askChoiceCancel (see main.js), but AskChoice has
  // already received the submit, so that stray cancel is dropped harmlessly.
  window.closeSurface && window.closeSurface();
}
