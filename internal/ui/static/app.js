'use strict';

let state = null;

// ── helpers ──────────────────────────────────────────────
const $ = (id) => document.getElementById(id);

function bytes(n) {
  if (!n) return '—';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${u[i]}`;
}

async function api(path, opts) {
  const res = await fetch(path, opts);
  const text = await res.text();
  let body = null;
  try { body = text ? JSON.parse(text) : null; } catch { /* not json */ }
  if (!res.ok) {
    throw new Error(body?.error?.message || text || `HTTP ${res.status}`);
  }
  return body;
}

const postModel = (path, model) => api(path, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ model }),
});

// ── tabs ─────────────────────────────────────────────────
function showTab(name) {
  document.querySelectorAll('.tab').forEach(
    (t) => t.classList.toggle('active', t.dataset.tab === name));
  document.querySelectorAll('.panel').forEach(
    (p) => p.classList.toggle('active', p.id === `tab-${name}`));
}
document.querySelectorAll('.tab').forEach(
  (t) => t.addEventListener('click', () => showTab(t.dataset.tab)));

// ── live state ───────────────────────────────────────────
function connect() {
  const es = new EventSource('/api/events');

  es.onmessage = (e) => {
    state = JSON.parse(e.data);
    render();
    $('statusDot').className = 'dot up';
    $('statusText').textContent = `serving on :${state.config.port}`;
  };

  es.onerror = () => {
    $('statusDot').className = 'dot down';
    $('statusText').textContent = 'disconnected';
    // EventSource retries on its own; don't stack up extra connections.
  };
}

function render() {
  if (!state) return;
  renderWarnings();
  renderSetup();
  renderModels();
  renderConnect();
  renderSettings();
}

function renderWarnings() {
  const box = $('warnings');
  box.innerHTML = '';
  (state.warnings || []).forEach((w) => {
    const d = document.createElement('div');
    d.className = 'banner warn';
    d.innerHTML = `<div>⚠</div><div><strong>${escapeHtml(w)}</strong></div>`;
    box.appendChild(d);
  });
}

function renderSetup() {
  const banner = $('setupBanner');
  const s = state.setup || {};
  const busy = !s.ready && s.stage !== 'idle' && s.stage !== 'ready';
  banner.hidden = s.ready || (!busy && s.stage !== 'failed');
  if (banner.hidden) return;

  $('setupStage').textContent =
    s.stage === 'failed' ? 'Setup failed' : `Setting up: ${s.stage}…`;
  $('setupErr').textContent = s.err || '';
}

// ── my models ────────────────────────────────────────────
function renderModels() {
  const list = $('modelList');
  const models = state.models || [];
  const resident = new Set((state.resident || []).map((r) => r.repo_id));

  $('modelsEmpty').hidden = models.length > 0;
  list.innerHTML = '';

  models.forEach((m) => {
    const card = document.createElement('div');
    card.className = 'card';

    const loaded = resident.has(m.repo_id);
    let pill = '';
    if (m.state === 'ready')       pill = loaded
      ? '<span class="pill loaded">loaded</span>'
      : '<span class="pill ready">ready</span>';
    else if (m.state === 'failed') pill = '<span class="pill failed">failed</span>';

    let info = '';
    if (m.state === 'downloading') {
      info = `downloading… ${m.progress.toFixed(0)}%`;
    } else if (m.state === 'failed') {
      info = escapeHtml(m.err || 'failed');
    } else {
      info = bytes(m.bytes);
    }

    card.innerHTML = `
      <div class="meta">
        <div class="name">${escapeHtml(m.repo_id)}${pill}</div>
        <div class="info">${info}</div>
        ${m.state === 'downloading'
          ? `<div class="bar"><i style="width:${m.progress}%"></i></div>` : ''}
      </div>
      <div class="actions"></div>`;

    const actions = card.querySelector('.actions');

    if (m.state === 'downloading') {
      actions.append(btn('Cancel', 'danger', () =>
        postModel('/api/models/cancel', m.repo_id).catch(alertErr)));
    } else if (m.state === 'failed') {
      actions.append(btn('Retry', '', () =>
        postModel('/api/models/download', m.repo_id).catch(alertErr)));
      // A failed/partial download has nothing worth protecting — remove at once.
      actions.append(btn('Remove', 'danger', () =>
        postModel('/api/models/delete', m.repo_id).catch(alertErr)));
    } else {
      actions.append(loaded
        ? btn('Unload', 'ghost', () =>
            postModel('/api/models/unload', m.repo_id).catch(alertErr))
        : btn('Load', 'ghost', () =>
            postModel('/api/models/load', m.repo_id).catch(alertErr)));
      // A ready model is real data, so require a deliberate second click.
      actions.append(confirmBtn('Delete', 'Confirm?', 'danger', () =>
        postModel('/api/models/delete', m.repo_id).catch(alertErr)));
    }
    list.appendChild(card);
  });
}

function btn(label, cls, onClick) {
  const b = document.createElement('button');
  b.textContent = label;
  if (cls) b.className = cls;
  b.addEventListener('click', onClick);
  return b;
}

// confirmBtn is an inline two-click confirmation. The first click arms the
// button (it shows confirmLabel for 3s); a second click within that window runs
// the action. This replaces window.confirm, which browsers let the user
// permanently suppress — silently turning destructive buttons into no-ops.
function confirmBtn(label, confirmLabel, cls, onConfirm) {
  const b = document.createElement('button');
  b.textContent = label;
  if (cls) b.className = cls;
  let armed = false;
  let timer = null;
  b.addEventListener('click', () => {
    if (armed) {
      clearTimeout(timer);
      armed = false;
      b.textContent = label;
      b.classList.remove('armed');
      onConfirm();
      return;
    }
    armed = true;
    b.textContent = confirmLabel;
    b.classList.add('armed');
    timer = setTimeout(() => {
      armed = false;
      b.textContent = label;
      b.classList.remove('armed');
    }, 3000);
  });
  return b;
}

// ── search ───────────────────────────────────────────────
$('searchForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const q = $('searchInput').value.trim();
  const box = $('searchResults');
  box.innerHTML = '<p class="hint">Searching…</p>';
  try {
    const data = await api(`/api/search?q=${encodeURIComponent(q)}`);
    renderSearch(data.results || []);
  } catch (err) {
    box.innerHTML = `<p class="msg err">${escapeHtml(err.message)}</p>`;
  }
});

function renderSearch(results) {
  const box = $('searchResults');
  box.innerHTML = '';
  if (!results.length) {
    box.innerHTML = '<p class="hint">No models found.</p>';
    return;
  }
  results.forEach((m) => {
    const card = document.createElement('div');
    card.className = 'card';
    const quant = m.quantization
      ? `<span class="pill quant">${escapeHtml(m.quantization)}</span>` : '';
    card.innerHTML = `
      <div class="meta">
        <div class="name">${escapeHtml(m.id)}${quant}</div>
        <div class="info">${(m.downloads || 0).toLocaleString()} downloads · ${m.likes || 0} likes</div>
      </div>
      <div class="actions"></div>`;

    const actions = card.querySelector('.actions');
    if (m.local_state === 'ready') {
      const b = btn('Downloaded', 'ghost', () => {});
      b.disabled = true;
      actions.append(b);
    } else if (m.local_state === 'downloading') {
      const b = btn('Downloading…', 'ghost', () => {});
      b.disabled = true;
      actions.append(b);
    } else {
      actions.append(btn('Download', '', async (ev) => {
        ev.target.disabled = true;
        ev.target.textContent = 'Starting…';
        try {
          await postModel('/api/models/download', m.id);
          showTab('models');
        } catch (err) {
          alertErr(err);
          ev.target.disabled = false;
          ev.target.textContent = 'Download';
        }
      }));
    }
    box.appendChild(card);
  });
}

// ── connect ──────────────────────────────────────────────
function renderConnect() {
  const box = $('endpoints');
  box.innerHTML = '';
  const eps = state.endpoints || [];
  eps.forEach((url) => {
    const row = document.createElement('div');
    row.className = 'endpoint';
    row.innerHTML = `<span>${escapeHtml(url)}</span>`;
    row.append(btn('Copy', 'ghost', () => navigator.clipboard.writeText(url)));
    box.appendChild(row);
  });

  const base = eps[0] || `http://localhost:${state.config.port}/v1`;
  const model = (state.models.find((m) => m.state === 'ready') || {}).repo_id
    || 'mlx-community/Qwen3-8B-4bit';
  const authCurl = state.config.api_key
    ? ` \\\n  -H "Authorization: Bearer YOUR_KEY"` : '';

  $('curlExample').textContent =
`curl ${base}/chat/completions \\
  -H "Content-Type: application/json"${authCurl} \\
  -d '{
    "model": "${model}",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'`;

  const authPy = state.config.api_key ? '"YOUR_KEY"' : '"not-needed"';
  $('pyExample').textContent =
`from openai import OpenAI

client = OpenAI(base_url="${base}", api_key=${authPy})

resp = client.chat.completions.create(
    model="${model}",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(resp.choices[0].message.content)`;
}

// ── settings ─────────────────────────────────────────────
let settingsTouched = false;
document.querySelectorAll('#settingsForm input, #settingsForm select')
  .forEach((el) => el.addEventListener('input', () => { settingsTouched = true; }));

function renderSettings() {
  // Don't stomp on what the user is typing while live updates arrive.
  if (settingsTouched) return;
  const c = state.config;
  $('setHost').value = c.host;
  $('setPort').value = c.port;
  $('setKey').value  = c.api_key || '';
  $('setIdle').value = c.idle_timeout_sec;
  $('setConc').value = c.decode_concurrency;
  $('setHF').value   = c.hf_token || '';
}

$('genKey').addEventListener('click', () => {
  const b = new Uint8Array(24);
  crypto.getRandomValues(b);
  const hex = Array.from(b).map((x) => x.toString(16).padStart(2, '0')).join('');
  $('setKey').value = `bh_${hex}`;
  settingsTouched = true;
});

$('settingsForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const msg = $('settingsMsg');
  const body = {
    host:               $('setHost').value,
    port:               parseInt($('setPort').value, 10),
    api_key:            $('setKey').value,
    idle_timeout_sec:   parseInt($('setIdle').value, 10) || 0,
    decode_concurrency: parseInt($('setConc').value, 10) || 1,
    hf_token:           $('setHF').value,
    advertise:          true,
  };
  try {
    const res = await api('/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    msg.className = 'msg';
    msg.textContent = res.restart
      ? 'Saved. Restart Gropius for the new address to take effect.'
      : 'Saved.';
    settingsTouched = false;
  } catch (err) {
    msg.className = 'msg err';
    msg.textContent = err.message;
  }
});

// ── misc ─────────────────────────────────────────────────
function escapeHtml(s) {
  const d = document.createElement('div');
  d.textContent = String(s ?? '');
  return d.innerHTML;
}

function alertErr(err) { alert(err.message || String(err)); }

connect();
