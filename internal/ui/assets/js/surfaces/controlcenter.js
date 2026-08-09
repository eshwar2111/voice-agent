// internal/ui/assets/js/surfaces/controlcenter.js
// The Control Center dashboard. Unlike command/result/approve, this surface
// stays a separately positioned/sized element (#dashboard, controlcenter.css)
// rather than rendering inside islandBody — resolve() (state.js) deliberately
// keeps the island itself compact while store.surface==='controlcenter', so
// this module operates on the static DOM in index.html instead of building
// nodes, exactly as it did before Task 7.
import { esc } from '../activities.js';
import { jlog, toast, openSurface, closeSurface } from '../main.js';

let settingsState = { llm_provider:'gemini', api_key:'', model:'', base_url:'',
                       enable_voice:false, privacy_mode:false };
let integrationTimer = null;

const dashCopy = {
  overview: ['Overview','Your assistant at a glance.','See readiness, wire in ecosystems, and tune behavior.'],
  integrations: ['Integrations','Connected ecosystems that feel first-class.','Google Workspace and Spotify behave like native assistant surfaces.'],
  model: ['Models','Shape how the assistant thinks.','Point the runtime at the provider and model that fit your setup.'],
  privacy: ['Privacy','Tune interaction and context posture.','Decide how voice and privacy defaults should feel.'],
};

export async function loadSettings(){
  if(!window.getSettings) return;
  const s = await window.getSettings();
  if(!s) return;
  settingsState = Object.assign(settingsState, s);
  providerSelect.value = settingsState.llm_provider || 'gemini';
  modelInput.value = settingsState.model || '';
  apiKeyInput.value = settingsState.api_key || '';
  baseUrlInput.value = settingsState.base_url || '';
  voiceToggle.classList.toggle('active', !!settingsState.enable_voice);
  privacyToggle.classList.toggle('active', !!settingsState.privacy_mode);
  metricProvider.textContent = (settingsState.llm_provider||'gemini').toUpperCase();
  overviewProvider.textContent = 'Model: '+(settingsState.llm_provider||'gemini');
  overviewVoice.textContent = 'Voice: '+(settingsState.enable_voice?'On':'Off');
  overviewPrivacy.textContent = 'Privacy: '+(settingsState.privacy_mode?'High':'Standard');
}

export function toggleFlag(f){
  if(f==='voice'){
    settingsState.enable_voice = !settingsState.enable_voice;
    voiceToggle.classList.toggle('active', settingsState.enable_voice);
    overviewVoice.textContent = 'Voice: '+(settingsState.enable_voice?'On':'Off');
  } else {
    settingsState.privacy_mode = !settingsState.privacy_mode;
    privacyToggle.classList.toggle('active', settingsState.privacy_mode);
    overviewPrivacy.textContent = 'Privacy: '+(settingsState.privacy_mode?'High':'Standard');
  }
}

export async function persistSettings(){
  settingsState.llm_provider = providerSelect.value;
  settingsState.model = modelInput.value.trim();
  settingsState.api_key = apiKeyInput.value.trim();
  settingsState.base_url = baseUrlInput.value.trim();
  const ok = await window.saveSettings(settingsState.llm_provider, settingsState.api_key,
    settingsState.model, settingsState.base_url, settingsState.enable_voice, settingsState.privacy_mode);
  if(ok){
    metricProvider.textContent = settingsState.llm_provider.toUpperCase();
    overviewProvider.textContent = 'Model: '+settingsState.llm_provider;
    toast('Settings saved');
  } else toast('Save failed');
}

function setConn(prefix, connected, text, pills){
  const badge=document.getElementById(prefix+'Badge'), txt=document.getElementById(prefix+'StatusText'),
        link=document.getElementById(prefix+'LinkBtn'), unlink=document.getElementById(prefix+'UnlinkBtn'),
        caps=document.getElementById(prefix+'Capabilities');
  badge.classList.remove('connected','disconnected');
  badge.classList.add(connected?'connected':'disconnected');
  badge.textContent = connected?'Connected':'Disconnected';
  txt.textContent = text;
  link.classList.toggle('hidden', connected);
  unlink.classList.toggle('hidden', !connected);
  if(caps && pills && pills.length) caps.innerHTML = pills.map(x=>'<span class="taglet">'+esc(x)+'</span>').join('');
}

export async function loadIntegrationStatusesDash(){
  let total = 0;
  if(window.getGoogleStatus){
    const g = await window.getGoogleStatus();
    const on = !!(g && g.connected);
    if(on) total++;
    // An EXPIRED link is not the same as never having linked. Showing the
    // generic invitation copy for a revoked token reads as "you never set this
    // up", so nobody realises a working integration has gone stale — meanwhile
    // every Calendar call fails in the background.
    setConn('google', on,
      on ? 'Connected as '+(g.email||'your account')+' — Docs, Sheets, Slides, Drive, Gmail, Calendar.'
         : (g && g.expired
              ? (g.reason || 'Sign-in expired — reconnect to restore Gmail, Calendar and Drive.')
              : 'Link Gmail, Calendar, Drive, Docs, Sheets, and Slides for a unified workspace assistant.'),
      g && g.workspace ? g.workspace : ['Gmail','Calendar','Drive','Docs','Sheets','Slides']);
  }
  if(window.getSpotifyStatus){
    const s = await window.getSpotifyStatus();
    const on = !!(s && s.connected);
    if(on) total++;
    setConn('spotify', on,
      on ? 'Connected as '+(s.display_name||'your account')+(s.product?' ('+s.product+')':'')
         : (s && s.expired
              ? (s.reason || 'Spotify sign-in expired — reconnect to restore playback control.')
              : 'Link Spotify for playback control, queueing, recommendations, and AI-curated sessions.'),
      s && s.capabilities ? s.capabilities : ['Playback','Queue','Recommendations','AI Curation']);
  }
  if(window.getMicrosoftStatus){
    const m = await window.getMicrosoftStatus();
    const on = !!(m && m.connected);
    if(on) total++;
    setConn('microsoft', on,
      on ? 'Microsoft account linked for Outlook and calendar workflows.'
         : 'Outlook and adjacent Microsoft workflows can be connected here.',
      m && m.workspace ? m.workspace : ['Outlook','Calendar','OneDrive']);
  }
  metricConnections.textContent = total+' active';
}

export function switchTab(tab){
  document.querySelectorAll('.nav button').forEach(b=>b.classList.toggle('active', b.dataset.tab===tab));
  document.querySelectorAll('.tab').forEach(t=>t.classList.remove('active'));
  document.getElementById('tab-'+tab).classList.add('active');
  dashKicker.textContent = dashCopy[tab][0];
  dashTitle.textContent = dashCopy[tab][1];
  dashSub.textContent = dashCopy[tab][2];
}

export async function openSettings(tab){
  jlog('openSettings');
  await loadSettings();
  openSurface('controlcenter', { tab: tab || 'overview' });
  switchTab(tab || 'overview');
  loadIntegrationStatusesDash();
  if(!integrationTimer) integrationTimer = setInterval(loadIntegrationStatusesDash, 4000);
}

export function closeSettings(){
  jlog('closeSettings');
  clearInterval(integrationTimer);
  integrationTimer = null;
  closeSurface();
}

window.switchTab = switchTab;
window.closeSettings = closeSettings;
window.persistSettings = persistSettings;
window.toggleFlag = toggleFlag;
window.openSettings = openSettings;
window.loadIntegrationStatusesDash = loadIntegrationStatusesDash;
