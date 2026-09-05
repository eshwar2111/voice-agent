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
    '<div class="rhead">' +
      '<div class="rhead-id"><span class="spark"><svg class="ico"><use href="#i-sparkle"/></svg></span><span>Answer</span></div>' +
      '<div class="rhead-actions">' +
        '<button class="btn ghost sm" type="button" id="resultCopyBtn">Copy</button>' +
        '<button class="iconbtn" type="button" id="resultCloseBtn" aria-label="Close" title="Close (Esc)">✕</button>' +
      '</div>' +
    '</div>' +
    '<div class="obody md" id="outputBody"><div class="md-inner"></div></div>' +
    '<div class="footer"><div class="actions">' +
      '<button class="btn ghost" type="button" id="resultAskBtn">Ask another</button>' +
      '<button class="btn primary" type="button" id="resultCloseBtn2">Close</button>' +
    '</div></div>';

  root.querySelector('#outputBody .md-inner').innerHTML = renderContent(latestOutput);
  root.querySelector('#resultCopyBtn').onclick = copyOutput;
  root.querySelector('#resultAskBtn').onclick = () => window.openSurface && window.openSurface('command');
  const close = () => window.closeSurface && window.closeSurface();
  root.querySelector('#resultCloseBtn').onclick = close;
  root.querySelector('#resultCloseBtn2').onclick = close;
  return root;
}

// appendDelta grows the answer as streamed chunks arrive. It writes accumulated
// text via textContent (never innerHTML) so partial, unbalanced Markdown can
// never inject markup mid-stream; the final full render (a fresh render() with
// the authoritative text) applies real Markdown formatting.
export function appendDelta(text){
  latestOutput += text || '';
  const inner = document.querySelector('#outputBody .md-inner');
  if(inner) inner.textContent = latestOutput;
}

// renderContent keeps the structured JSON renderers (calendar / gmail / system),
// and otherwise treats the text as Markdown.
function renderContent(text){
  try{
    const p = JSON.parse(text);
    if(p && p.type==='calendar_list' && Array.isArray(p.data)){
      return '<div class="list">'+p.data.map(x=>'<div class="item"><div class="item-title">'+esc(x.summary||'Untitled event')+'</div><div class="item-meta">'+esc(x.startTime||'')+(x.location?' · '+esc(x.location):'')+'</div></div>').join('')+'</div>';
    }
    if(p && p.type==='gmail_list' && Array.isArray(p.data)){
      return '<div class="list">'+p.data.map(x=>'<div class="item"><div class="item-title">'+esc(x.subject||'No subject')+'</div><div class="item-meta">'+esc(x.from||'Unknown')+(x.date?' · '+esc(x.date):'')+'</div>'+(x.snippet?'<div class="item-snippet">'+esc(x.snippet)+'</div>':'')+'</div>').join('')+'</div>';
    }
    if(p && p.type==='system_status' && p.data){
      return '<div class="list"><div class="item"><div class="item-title">CPU</div><div class="item-meta">'+Number(p.data.cpu||0)+'% active</div></div><div class="item"><div class="item-title">Memory</div><div class="item-meta">'+(p.data.ramFree||0)+' GB free of '+(p.data.ramTotal||0)+' GB</div></div></div>';
    }
  }catch(e){}
  return renderMarkdown(text);
}

// ─── Markdown ────────────────────────────────────────────────────────────────
// A small, safe Markdown renderer. Everything is HTML-escaped FIRST (via esc),
// then a fixed set of inline/block transforms add known-safe tags — so model
// output can never inject markup. Supports: fenced + inline code, headings,
// bold/italic, links (https only), and bullet / numbered lists.

// mdInline transforms already-escaped text. Order matters: code first (so its
// contents aren't touched), then bold before italic (** before *).
function mdInline(s){
  return s
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>')
    .replace(/(^|[^_])_([^_\n]+)_(?!\w)/g, '$1<em>$2</em>')
    .replace(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g,
             '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
}

function renderMarkdown(raw){
  const escd = esc(String(raw || ''));
  // Split on fenced code blocks; odd segments are code, rendered verbatim.
  const parts = escd.split(/```/);
  let html = '';
  for(let i=0;i<parts.length;i++){
    if(i % 2 === 1){
      const body = parts[i].replace(/^[a-zA-Z0-9+_-]*\n/, '').replace(/\n$/, '');
      html += '<pre class="code"><code>'+body+'</code></pre>';
    } else {
      html += renderBlocks(parts[i]);
    }
  }
  return html || '<p></p>';
}

function renderBlocks(text){
  const lines = text.split('\n');
  let out = '', list = null, para = [];
  const flushList = () => { if(list){ out += '</'+list+'>'; list = null; } };
  const flushPara = () => { if(para.length){ out += '<p>'+mdInline(para.join(' '))+'</p>'; para = []; } };
  for(const ln of lines){
    const t = ln.trim();
    if(t === ''){ flushPara(); flushList(); continue; }
    let m;
    if((m = t.match(/^(#{1,3})\s+(.*)$/))){
      flushPara(); flushList();
      const lvl = Math.min(4, m[1].length + 1); // #→h2 (h1 is the panel chrome)
      out += '<h'+lvl+'>'+mdInline(m[2])+'</h'+lvl+'>';
    } else if((m = t.match(/^[-*]\s+(.*)$/))){
      flushPara();
      if(list !== 'ul'){ flushList(); out += '<ul>'; list = 'ul'; }
      out += '<li>'+mdInline(m[1])+'</li>';
    } else if((m = t.match(/^\d+\.\s+(.*)$/))){
      flushPara();
      if(list !== 'ol'){ flushList(); out += '<ol>'; list = 'ol'; }
      out += '<li>'+mdInline(m[1])+'</li>';
    } else {
      flushList();
      para.push(t);
    }
  }
  flushPara(); flushList();
  return out;
}

function copyOutput(){
  if(!latestOutput) return;
  navigator.clipboard.writeText(latestOutput).then(() => toast('Copied'));
}
