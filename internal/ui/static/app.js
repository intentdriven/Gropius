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
  // The request rows are fetched rather than pushed: they are far too many to
  // ride the state snapshot the rest of the panel redraws from, so they are
  // asked for while the view that shows them is open and not otherwise.
  watchStats(name === 'stats');
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
  // Independent of renderSettings, which returns early while the form is
  // being edited: the list of models to choose from is live state, not a
  // value the user is in the middle of typing.
  refreshOverrideModels();
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
  $('setStats').checked = !!c.statistics;
  writeSampling('input', c.sampling);
  overrides = { ...(c.model_sampling || {}) };
  renderOverrides();
  renderMergeSwitches();
}

// renderMergeSwitches draws one box per downloaded model. Merging is per model
// because it is a fact about that model's chat template, and because it is the
// one setting that has Gropius read a request's instructions at all.
function renderMergeSwitches() {
  const box = $('mergeList');
  const models = state.models || [];
  const per = state.config.per_model || {};
  box.innerHTML = '';
  if (!models.length) {
    box.innerHTML = '<p class="hint">Download a model and it appears here.</p>';
    return;
  }
  models.forEach((m) => {
    const row = document.createElement('label');
    row.className = 'switch';
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.dataset.model = m.repo_id;
    cb.checked = !!(per[m.repo_id] && per[m.repo_id].merge_system_messages);
    cb.addEventListener('change', () => { settingsTouched = true; });
    const name = document.createElement('span');
    name.textContent = m.repo_id;
    row.appendChild(cb);
    row.appendChild(name);
    box.appendChild(row);
  });
}

// mergeBoxes are the boxes drawn above, one per model the form lists.
function mergeBoxes() {
  return Array.from(document.querySelectorAll('#mergeList input[type=checkbox]'));
}

// listedMergeModels lists the models the form drew a box for.
function listedMergeModels() {
  return mergeBoxes().map((cb) => cb.dataset.model);
}

// checkedMergeModels lists the models whose box is ticked.
function checkedMergeModels() {
  return mergeBoxes().filter((cb) => cb.checked).map((cb) => cb.dataset.model);
}

// perModelSettings returns the whole per-model map a save posts. The server
// replaces what it holds with this, so a model whose box is clear is simply
// left out and its merging goes off — a map cannot be switched off by omission
// any other way.
//
// Two things are therefore carried through rather than rebuilt. Settings this
// form does not own stay on the model that has them, so ticking a box never
// wipes a model's other settings. And a model the form does not list keeps
// everything it has, because a model can be given settings before it is
// downloaded and a form with no box for it has nothing to say about it.
function perModelSettings(current, listed, checked) {
  const shown = new Set(listed || []);
  const out = {};
  Object.keys(current || {}).forEach((id) => {
    if (!shown.has(id)) {
      out[id] = current[id];
      return;
    }
    const rest = Object.assign({}, current[id]);
    delete rest.merge_system_messages;
    if (Object.keys(rest).length) out[id] = rest;
  });
  (checked || []).forEach((id) => {
    out[id] = Object.assign({}, out[id] || {}, { merge_system_messages: true });
  });
  return out;
}

function renderOverrides() {
  renderOverrideList();
  refreshOverrideModels();
  prefillOverride();
}

function renderOverrideList() {
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

}

// refreshOverrideModels rebuilds the list of models that can be given an
// override, keeping the current selection. It runs on every state frame, not
// only when the form is redrawn, so a model that finishes downloading while
// the form is being edited can be chosen without reloading the page.
function refreshOverrideModels() {
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

// prefillOverride shows what is stored for the selected model. Setting an
// override replaces it whole, so the fields have to start from the saved
// values — otherwise changing a temperature quietly drops the token budget
// saved beside it.
function prefillOverride() {
  writeSampling('over', overrides[$('ovModel').value]);
}

// foldPendingOverride takes whatever is in the per-model fields and makes it
// the selected model's override; every field blank removes it. Called both
// from the button and from the form's submit, because the fields sit inside
// the settings form and pressing Save (or Enter in a number field) must not
// throw away what is typed in them.
function foldPendingOverride() {
  const id = $('ovModel').value;
  if (!id) return;
  const values = readSampling('over');
  if (isEmptySampling(values)) {
    delete overrides[id];
  } else {
    overrides[id] = values;
  }
}

$('ovModel').addEventListener('change', prefillOverride);

$('ovApply').addEventListener('click', () => {
  foldPendingOverride();
  settingsTouched = true;
  renderOverrides();
});

$('setStats').addEventListener('change', () => { settingsTouched = true; });

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
  // Anything typed in the per-model fields counts, whether or not Set override
  // was pressed.
  foldPendingOverride();
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
    statistics:         $('setStats').checked,
    // A blank sampling field is sent as null, not as zero: the model server is
    // handed a flag only for a parameter that has a value.
    sampling:           readSampling('input'),
    model_sampling:     overrides,
    per_model:         perModelSettings(state.config.per_model, listedMergeModels(), checkedMergeModels()),
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

// ── statistics ───────────────────────────────────────────
// Nothing here is shown, and nothing is fetched, until the operator turns
// recording on: with the switch off the endpoint answers with an empty view
// and the panel says so.

let statsTimer = null;

// watchStats starts or stops the polling that keeps the Statistics view fresh
// while it is the visible one.
function watchStats(visible) {
  if (statsTimer) { clearInterval(statsTimer); statsTimer = null; }
  if (!visible) return;
  refreshStats();
  statsTimer = setInterval(refreshStats, 2000);
}

async function refreshStats() {
  try {
    renderStats(await api('/api/stats'));
  } catch {
    // A panel that cannot reach its own server already says "disconnected" at
    // the top; a second alert about it would be noise.
  }
}

function renderStats(view) {
  const on = !!(view && view.enabled);
  $('statsOff').hidden = on;
  $('statsBody').hidden = !on;
  if (!on) return;

  const totals = recentTotals(view.rollups || [], Math.floor(Date.now() / 1000));
  $('statsTotals').textContent =
    `Last hour: ${totals.hourRequests} request${totals.hourRequests === 1 ? '' : 's'}, ` +
    `${totals.hourCompletion} tokens generated · ` +
    `Last 24 hours: ${totals.dayRequests} request${totals.dayRequests === 1 ? '' : 's'}, ` +
    `${totals.dayCompletion} tokens generated`;

  $('statsModels').innerHTML = (view.models || []).length
    ? (view.models || []).map(modelStatsCard).join('')
    : '<p class="hint">No requests yet.</p>';

  // Newest first: the request a reader wants is almost always the last one.
  const rows = (view.requests || []).slice().reverse();
  $('statsRows').innerHTML = rows.map((r) => {
    const c = requestRow(r);
    const bad = c.failed ? ' bad' : '';
    return `<tr>
      <td>${c.time}</td>
      <td>${escapeHtml(c.model)}</td>
      <td>${c.mode}</td>
      <td class="${c.failed ? 'bad' : ''}">${c.outcome}</td>
      <td class="figure${bad}">${c.tokens}</td>
      <td class="figure${bad}">${c.first}</td>
      <td class="figure${bad}">${c.rate}</td>
      <td class="figure${bad}">${c.waited}</td>
      <td class="figure${bad}">${c.total}</td>
    </tr>`;
  }).join('');
}

// recentTotals adds the minute buckets up over the last hour and the last day.
// The buckets are the only thing that reaches back further than the thousand
// rows the ring holds, which at a busy minute is not far at all.
function recentTotals(rollups, nowSeconds) {
  const hourFrom = nowSeconds - 3600;
  const dayFrom = nowSeconds - 86400;
  const t = { hourRequests: 0, hourCompletion: 0, dayRequests: 0, dayCompletion: 0 };
  rollups.forEach((b) => {
    if (b.minute < dayFrom) return;
    t.dayRequests += b.requests;
    t.dayCompletion += b.completion_tokens;
    if (b.minute < hourFrom) return;
    t.hourRequests += b.requests;
    t.hourCompletion += b.completion_tokens;
  });
  return t;
}

// modelStatsCard is one model's totals since recording was turned on.
function modelStatsCard(m) {
  const failed = (m.requests || 0) - ((m.by_class || {}).ok || 0);
  const last = generationRate({
    class: 'ok',
    completion_tokens: m.last_completion_tokens,
    first_token_ms: m.last_first_token_ms,
    duration_ms: m.last_duration_ms,
  });
  const figures = [
    `${m.requests} request${m.requests === 1 ? '' : 's'}`,
    failed ? `${failed} did not answer` : null,
    `${m.prompt_tokens} tokens in · ${m.completion_tokens} out`,
    m.last_first_token_ms >= 0 ? `last first token ${millis(m.last_first_token_ms)}` : null,
    last !== '—' ? `last rate ${last}` : null,
    m.last_duration_ms ? `last request ${millis(m.last_duration_ms)}` : null,
    m.loads ? `loaded ${m.loads}×` : null,
    m.failed_loads ? `${m.failed_loads} failed to load` : null,
    m.evictions ? `evicted ${m.evictions}×` : null,
    m.last_load_ms ? `last load ${millis(m.last_load_ms)}` : null,
  ].filter(Boolean).join(' · ');
  return `<div class="statcard"><div class="name">${escapeHtml(m.model || '—')}</div>` +
    `<div class="figures">${figures}</div></div>`;
}

// requestRow is the whole of one row of the request table: a pure function of
// one record, so what a reader sees can be asserted without a DOM (see
// stats_test.go). Every cell is a figure, a fixed outcome word, or the id of a
// model this Mac holds — there is nothing else in a record to show.
function requestRow(r) {
  const answered = r.class === 'ok';
  const d = new Date(r.at * 1000);
  const pad = (n) => String(n).padStart(2, '0');
  // Both waits are a wait for the model rather than for the answer, so they
  // are one cell: a reader wants to know how much of the total was spent
  // before generation began, not which queue it was spent in.
  const waited = (r.queue_wait_ms || 0) + (r.load_wait_ms || 0);
  return {
    time: `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`,
    model: r.model || '—',
    mode: r.streamed ? 'stream' : 'once',
    outcome: outcomeLabel(r.class),
    failed: !answered,
    tokens: answered ? `${r.prompt_tokens} / ${r.completion_tokens}` : '—',
    first: r.first_token_ms >= 0 ? millis(r.first_token_ms) : '—',
    rate: generationRate(r),
    waited: waited ? millis(waited) : '—',
    total: millis(r.duration_ms),
  };
}

// outcomeLabel says in words how a request ended. The recorder's own names are
// for the record and for whatever reads it later; a reader of the panel should
// not have to learn them.
function outcomeLabel(c) {
  return {
    ok: 'answered',
    client_error: 'rejected',
    upstream_status: 'model refused',
    busy: 'too busy',
    refused: 'no room',
    launch_failed: 'could not start',
    not_ready: 'never ready',
    unreachable: 'no answer',
    cancelled: 'client left',
    gateway_error: 'Gropius failed',
  }[c] || 'unknown';
}

// generationRate is the tokens after the first over the time spent generating
// them, which is what vLLM, the OpenTelemetry conventions and LM Studio all
// mean by tokens per second. A request with no first token, or with one token,
// has no rate at all rather than a figure that means something else.
function generationRate(r) {
  if (r.class !== 'ok' || r.first_token_ms < 0 || r.completion_tokens < 2) return '—';
  const generating = (r.duration_ms - r.first_token_ms) / 1000;
  if (generating <= 0) return '—';
  return `${((r.completion_tokens - 1) / generating).toFixed(1)} tok/s`;
}

// millis renders a duration the way a reader reads one: milliseconds while
// they are small enough to matter, seconds once they are not.
function millis(ms) {
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`;
}

// ── misc ─────────────────────────────────────────────────
function escapeHtml(s) {
  const d = document.createElement('div');
  d.textContent = String(s ?? '');
  return d.innerHTML;
}

function alertErr(err) { alert(err.message || String(err)); }

connect();
