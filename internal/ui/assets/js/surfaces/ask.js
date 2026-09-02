// internal/ui/assets/js/surfaces/ask.js
// The clarification prompt: shown when a tool needs a value the request didn't
// specify (e.g. a filename). render(payload) is called by main.js when
// store.surface==='ask', with payload === { question }. Submitting sends the
// answer back to the blocked Go AskText via window.askTextSubmit; cancelling
// (button, Esc, or any close) sends window.askTextCancel so AskText never hangs.
import { esc } from '../activities.js';

let inputEl = null;

export function render(payload){
  const q = (payload && payload.question) || 'What should I use?';
  const root = document.createElement('div');
  root.className = 'surface-command surface-ask';
  root.innerHTML =
    '<div class="phead"><div><div class="eyebrow">One quick thing</div>' +
      '<h1>'+esc(q)+'</h1>' +
      '<p>Type your answer and press Enter to continue.</p></div></div>' +
    '<textarea id="askInput" placeholder="Your answer…"></textarea>' +
    '<div class="footer"><div class="actions">' +
      '<button class="btn ghost" type="button" id="askCancelBtn">Cancel</button>' +
      '<button class="btn primary" type="button" id="askSubmitBtn">Continue</button>' +
    '</div><div class="hint"><div>Enter to continue.</div><div><kbd>Esc</kbd> cancel</div></div></div>';

  inputEl = root.querySelector('#askInput');
  root.querySelector('#askSubmitBtn').onclick = submit;
  root.querySelector('#askCancelBtn').onclick = () => window.closeSurface && window.closeSurface();
  inputEl.addEventListener('keydown', (e) => {
    if(e.key === 'Enter' && !e.shiftKey){ e.preventDefault(); submit(); }
  });
  setTimeout(() => inputEl && inputEl.focus(), 80);
  return root;
}

function submit(){
  if(!inputEl) return;
  const v = inputEl.value.trim();
  if(!v) return;
  window.askTextSubmit && window.askTextSubmit(v);
  // closeSurface would also fire askTextCancel (see main.js), but AskText has
  // already received the submit, so that stray cancel is dropped harmlessly.
  window.closeSurface && window.closeSurface();
}
