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

// contextLabel renders a model's architectural maximum context for its card.
// The label says "max context" so the figure is not read as the window this
// Mac can hold at once, which is a smaller and separate number. The
// abbreviation rounds DOWN: rounding 262,143 up to 256K would show a token
// more than the model declares, and under-reporting is the safe direction for
// a reader sizing a prompt from the card. The result is composed from a
// number, so it is safe in the innerHTML the card is built from; anything
// that is not a positive number gives no label at all.
function contextLabel(m) {
  const n = m.context_length;
  if (typeof n !== 'number' || !Number.isFinite(n) || n <= 0) return '';
  return `max context ${n >= 1024 ? `${Math.floor(n / 1024)}K` : n}`;
}

// modelInfoLine is the whole info line of a model's card: what it says
// about a download in flight, a failure, or a model on disk. It is a pure
// function of the model, so the panel's one piece of real logic can be
// tested without a DOM (see panel_test.go); renderModels does nothing with
// it but place the string it returns.
function modelInfoLine(m) {
  if (m.state === 'downloading') {
    const of = m.size_bytes ? ` of ${bytes(m.size_bytes)}` : '';
    return `downloading… ${m.progress.toFixed(0)}%${of}`;
  }
  if (m.state === 'failed') return escapeHtml(m.err || 'failed');
  const ctx = contextLabel(m);
  return ctx ? `${bytes(m.bytes)} · ${ctx}` : bytes(m.bytes);
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

    const info = modelInfoLine(m);

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
      // Cancel abandons the download outright: stop it and remove the partial
      // files, rather than leaving a lingering "failed" row behind.
      actions.append(btn('Cancel', 'danger', () =>
        postModel('/api/models/delete', m.repo_id).catch(alertErr)));
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
    renderSearch(data);
  } catch (err) {
    box.innerHTML = `<p class="msg err">${escapeHtml(err.message)}</p>`;
  }
});

function renderSearch(data) {
  const box = $('searchResults');
  const results = (data && data.results) || [];
  const machine = data && data.machine;
  const hidden = (data && data.hidden) || 0;
  box.innerHTML = '';

  // A note describing this Mac and how many models were hidden as too large.
  if (machine && machine.total_ram) {
    const note = document.createElement('p');
    note.className = 'hint';
    let text = `This Mac: ${bytes(machine.total_ram)} RAM · ${bytes(machine.free_disk)} free.`;
    if (hidden > 0) {
      text += ` ${hidden} model${hidden === 1 ? '' : 's'} hidden (too large to run or store here).`;
    } else {
      text += ' Showing models that fit.';
    }
    note.textContent = text;
    box.appendChild(note);
  }

  if (!results.length) {
    const p = document.createElement('p');
    p.className = 'hint';
    p.textContent = hidden > 0
      ? 'No models small enough for this Mac matched your search.'
      : 'No models found.';
    box.appendChild(p);
    return;
  }
  results.forEach((m) => {
    const card = document.createElement('div');
    card.className = 'card';
    const quant = m.quantization
      ? `<span class="pill quant">${escapeHtml(m.quantization)}</span>` : '';
    const size = m.size_bytes ? ` · ${bytes(m.size_bytes)}` : '';
    card.innerHTML = `
      <div class="meta">
        <div class="name">${escapeHtml(m.id)}${quant}</div>
        <div class="info">${(m.downloads || 0).toLocaleString()} downloads · ${m.likes || 0} likes${size}</div>
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

// The sampling fields, paired with the config keys they carry. Each is a
// number or nothing at all: blank means "pass no flag", which is not the same
// as zero — zero is a real temperature.
const SAMPLING_FIELDS = [
  { key: 'temperature', input: 'setTemp',      over: 'ovTemp',      integer: false },
  { key: 'top_p',       input: 'setTopP',      over: 'ovTopP',      integer: false },
  { key: 'top_k',       input: 'setTopK',      over: 'ovTopK',      integer: true },
  { key: 'min_p',       input: 'setMinP',      over: 'ovMinP',      integer: false },
  { key: 'max_tokens',  input: 'setMaxTokens', over: 'ovMaxTokens', integer: true },
];

// The per-model overrides being edited. Held here rather than read back off
// the form, so removing one and saving is a single action.
let overrides = {};

// numberOrNull reads one numeric input. A blank field is null — the key is
// still sent, so clearing a field clears the stored value.
function numberOrNull(id, integer) {
  const raw = $(id).value.trim();
  if (raw === '') return null;
  const n = Number(raw);
  if (!Number.isFinite(n)) return null;
  return integer ? Math.trunc(n) : n;
}

function readSampling(which) {
  const out = {};
  SAMPLING_FIELDS.forEach((f) => { out[f.key] = numberOrNull(f[which], f.integer); });
  return out;
}

function writeSampling(which, values) {
  const v = values || {};
  SAMPLING_FIELDS.forEach((f) => {
    $(f[which]).value = (v[f.key] === undefined || v[f.key] === null) ? '' : v[f.key];
  });
}

// isEmptySampling reports whether nothing at all is set, so "set override"
// with every field blank removes the override instead of storing an empty one.
function isEmptySampling(values) {
  return SAMPLING_FIELDS.every((f) => values[f.key] === null || values[f.key] === undefined);
}

function describeSampling(values) {
  return SAMPLING_FIELDS
    .filter((f) => values[f.key] !== null && values[f.key] !== undefined)
    .map((f) => `${f.key} ${values[f.key]}`)
    .join(' · ');
}

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
  writeSampling('input', c.sampling);
  overrides = { ...(c.model_sampling || {}) };
  renderOverrides();
}

function renderOverrides() {
  const list = $('overrideList');
  list.innerHTML = '';
  Object.keys(overrides).sort().forEach((id) => {
    const row = document.createElement('div');
    row.className = 'override';
    const meta = document.createElement('div');
    const name = document.createElement('div');
    name.className = 'name';
    name.textContent = id;
    const values = document.createElement('div');
    values.className = 'values';
    values.textContent = describeSampling(overrides[id]) || 'no parameters set';
    meta.append(name, values);
    row.append(meta, btn('Remove', 'ghost', () => {
      delete overrides[id];
      settingsTouched = true;
      renderOverrides();
    }));
    list.appendChild(row);
  });

  // Offer every model on this Mac, plus any model an override already names
  // (one whose files have since been deleted still has a saved override).
  const select = $('ovModel');
  const chosen = select.value;
  const ids = new Set((state.models || []).map((m) => m.repo_id));
  Object.keys(overrides).forEach((id) => ids.add(id));
  select.innerHTML = '';
  [...ids].sort().forEach((id) => {
    const opt = document.createElement('option');
    opt.value = id;
    opt.textContent = id;
    select.appendChild(opt);
  });
  if (chosen && ids.has(chosen)) select.value = chosen;
}

$('ovApply').addEventListener('click', () => {
  const id = $('ovModel').value;
  if (!id) return;
  const values = readSampling('over');
  if (isEmptySampling(values)) {
    delete overrides[id];
  } else {
    overrides[id] = values;
  }
  writeSampling('over', {});
  settingsTouched = true;
  renderOverrides();
});

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
  // Only the fields this form owns. Anything omitted (advertise, preload) is
  // preserved server-side — sending advertise:true here used to silently
  // re-enable LAN advertising on every save.
  const body = {
    host:               $('setHost').value,
    port:               parseInt($('setPort').value, 10),
    api_key:            $('setKey').value,
    idle_timeout_sec:   parseInt($('setIdle').value, 10) || 0,
    decode_concurrency: parseInt($('setConc').value, 10) || 1,
    hf_token:           $('setHF').value,
    // A blank sampling field is sent as null, not as zero: the model server is
    // handed a flag only for a parameter that has a value.
    sampling:           readSampling('input'),
    model_sampling:     overrides,
  };
  try {
    const res = await api('/api/settings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    msg.className = 'msg';
    const parts = ['Saved.'];
    if (res.restart) parts.push('Restart Gropius for the change to take effect.');
    // Sampling defaults are set when a model server starts, so a model that is
    // already loaded keeps the values it started with.
    if (res.reload_models && res.reload_models.length) {
      parts.push(`Load ${res.reload_models.join(', ')} again to serve with the new sampling defaults.`);
    }
    msg.textContent = parts.join(' ');
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
