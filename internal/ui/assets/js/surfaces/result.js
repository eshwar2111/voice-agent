// internal/ui/assets/js/surfaces/result.js
// The result sheet: shows a completed command's output. render(payload) is
// called by main.js's renderContentFor whenever store.surface==='result',
// with payload === { text }, exactly as passed to openSurface('result', ...)
// by the 'surface:open' bridge handler in main.js.
import { esc } from '../activities.js';
import { toast } from '../main.js';

let latestOutput = '';

export function render(payload){
  latestOutput = (payload && payload.text) || '';
  const root = document.createElement('div');
  root.className = 'surface-result';
  root.innerHTML =
    '<div class="phead"><div><div class="eyebrow">Response</div><h1>Result</h1>' +
    '<p>Answers and connected-app results appear here.</p></div>' +
    '<button class="btn ghost" type="button" id="resultCopyBtn">Copy</button></div>' +
    '<div class="obody" id="outputBody"></div>' +
    '<div class="footer"><div class="actions">' +
    '<button class="btn ghost" type="button" id="resultAskBtn">Ask another</button>' +
    '</div></div>';

  root.querySelector('#outputBody').innerHTML = renderContent(latestOutput);
  root.querySelector('#resultCopyBtn').onclick = copyOutput;
  root.querySelector('#resultAskBtn').onclick = () => window.openSurface && window.openSurface('command');
  return root;
}

function renderText(t){
  return '<div>'+esc(t).replace(/\n/g,'<br/>').replace(/\*\*(.*?)\*\*/g,'<strong>$1</strong>')+'</div>';
}

function renderContent(text){
  try{
    const p = JSON.parse(text);
    if(p && p.type==='calendar_list' && Array.isArray(p.data)){
      return '<div class="list">'+p.data.map(x=>'<div class="item"><div class="item-title">'+esc(x.summary||'Untitled event')+'</div><div class="item-meta">'+esc(x.startTime||'')+(x.location?' · '+esc(x.location):'')+'</div></div>').join('')+'</div>';
    }
    if(p && p.type==='gmail_list' && Array.isArray(p.data)){
      return '<div class="list">'+p.data.map(x=>'<div class="item"><div class="item-title">'+esc(x.subject||'No subject')+'</div><div class="item-meta">'+esc(x.from||'Unknown')+(x.date?' · '+esc(x.date):'')+'</div>'+(x.snippet?'<div style="margin-top:8px;color:var(--ink-2);font-size:12px">'+esc(x.snippet)+'</div>':'')+'</div>').join('')+'</div>';
    }
    if(p && p.type==='system_status' && p.data){
      return '<div class="list"><div class="item"><div class="item-title">CPU</div><div class="item-meta">'+Number(p.data.cpu||0)+'% active</div></div><div class="item"><div class="item-title">Memory</div><div class="item-meta">'+(p.data.ramFree||0)+' GB free of '+(p.data.ramTotal||0)+' GB</div></div></div>';
    }
  }catch(e){}
  return renderText(text);
}

function copyOutput(){
  if(!latestOutput) return;
  navigator.clipboard.writeText(latestOutput).then(() => toast('Copied'));
}
