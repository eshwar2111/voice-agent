// internal/ui/assets/js/surfaces/command.js
// The command sheet: a free-text box for typed requests. render() is called
// by main.js's renderContentFor whenever store.surface==='command' (resolve()
// has already decided that means presence="sheet"); this module only builds
// the DOM node and wires its own listeners — it never touches geometry.
import { jlog } from '../main.js';

let inputEl = null;

export function render(){
  const root = document.createElement('div');
  root.className = 'surface-command';
  root.innerHTML =
    '<div class="phead"><div><div class="eyebrow">Command</div><h1>Ask anything.</h1>' +
    '<p>Type a request — apps, files, the web, or desktop actions. Prefix with “ai” for a free-form prompt.</p></div>' +
    '<div class="chip">⌘ Space</div></div>' +
    '<textarea id="commandInput" placeholder="Try: open notepad · summarize this · play focus music"></textarea>' +
    '<div class="footer">' +
    '<div class="suggestions">' +
    '<button class="btn ghost" type="button" data-fill="ai give me my Google Workspace brief">Workspace brief</button>' +
    '<button class="btn ghost" type="button" data-fill="ai draft a Google Doc for sprint planning">Draft a doc</button>' +
    '<button class="btn ghost" type="button" data-fill="ai create a slide deck about quarterly goals">Create slides</button>' +
    '<button class="btn ghost" type="button" data-fill="ai play something for deep focus on spotify">Spotify focus</button>' +
    '</div>' +
    '<div class="hint"><div>Enter to submit · Shift+Enter for a new line.</div><div><kbd>Esc</kbd> collapse</div></div>' +
    '</div>';

  inputEl = root.querySelector('#commandInput');
  root.querySelectorAll('[data-fill]').forEach(b => {
    b.onclick = () => fillSuggestion(b.dataset.fill);
  });
  inputEl.addEventListener('keydown', onKeydown);
  setTimeout(() => inputEl && inputEl.focus(), 80);
  return root;
}

function fillSuggestion(v){
  if(!inputEl) return;
  inputEl.value = v;
  inputEl.focus();
}

function submitCurrentCommand(){
  if(!inputEl) return;
  const v = inputEl.value.trim();
  if(!v) return;
  jlog('submit "'+v+'"');
  window.submitCommand && window.submitCommand(v);
  inputEl.value = '';
  window.closeSurface && window.closeSurface();
  window.updateUI && window.updateUI('thinking','Dispatching…');
}

async function onKeydown(e){
  if(e.key==='Enter' && !e.shiftKey){ e.preventDefault(); submitCurrentCommand(); return }
  if(e.key==='ArrowUp' && e.altKey && window.getPrevCommand){
    e.preventDefault();
    const p = await window.getPrevCommand();
    if(p) e.target.value = p;
  }
  if(e.key==='ArrowDown' && e.altKey && window.getNextCommand){
    e.preventDefault();
    e.target.value = await window.getNextCommand();
  }
}
