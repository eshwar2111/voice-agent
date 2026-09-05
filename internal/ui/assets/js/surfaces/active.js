// internal/ui/assets/js/surfaces/active.js
// The "Active" tasks surface (satellites): when several activities run at once,
// the island shows a count badge that opens this on-demand list. It reads the
// live activity snapshot directly, so it needs no payload from Go.
import { esc, liveSummaries } from '../activities.js';

const META = {
  'agent.run':          { ic: '✦', name: 'Working' },
  'trust.approval':     { ic: '⚠', name: 'Approval needed' },
  'task.progress':      { ic: '⚙', name: 'Task' },
  'spotify.nowplaying': { ic: '🎵', name: 'Music' },
  'ambient.nudge':      { ic: '💡', name: 'Suggestion' },
  'timer':              { ic: '⏱', name: 'Timer' },
  'meeting':            { ic: '📅', name: 'Meeting' },
};

function summarize(a) {
  const key = a.kind || a.id;
  const m = META[key] || META[a.id] || { ic: '•', name: key };
  const d = a.data || {};
  const title = d.title || d.label || d.track || m.name;
  const status = d.note || d.artist || (d.phase && d.phase !== 'running' ? d.phase : '')
    || (typeof d.minutes === 'number' ? 'in ' + d.minutes + 'm' : '');
  return { ic: m.ic, title, status };
}

export function render() {
  const items = liveSummaries();
  const root = document.createElement('div');
  root.className = 'surface-active';
  const rows = items.length
    ? items.map((a) => {
        const { ic, title, status } = summarize(a);
        return '<div class="sat"><span class="si">' + ic + '</span>' +
          '<div class="st"><div class="stt">' + esc(title) + '</div>' +
          (status ? '<div class="sts">' + esc(status) + '</div>' : '') + '</div></div>';
      }).join('')
    : '<div class="sat-empty">Nothing active right now.</div>';
  root.innerHTML =
    '<div class="rhead"><div class="rhead-id"><span>Active · ' + items.length + '</span></div>' +
    '<button class="iconbtn" id="activeClose" aria-label="Close" title="Close (Esc)">✕</button></div>' +
    '<div class="sats">' + rows + '</div>';
  root.querySelector('#activeClose').onclick = () => window.closeSurface && window.closeSurface();
  return root;
}
