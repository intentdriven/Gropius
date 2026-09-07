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

// foldRepoID is config.FoldRepoID: the one rule for when two repo ids name the
// same model. The panel joins pins against model ids, and a pin can be carried
// in a spelling the registry does not use — set before the model was
// downloaded — until the next save reconciles it, so the join folds like every
// other join on a repo id in this project does.
function foldRepoID(id) { return String(id ?? '').toLowerCase(); }

// pinLabel is the pill a model's card carries when it is pinned: which of the
// two things being pinned means right now, or '' when it is not. A model that
// is protected but that nothing has loaded says so, because an operator
// reading "pinned" would otherwise assume it was running.
function pinLabel(m, pinned, loaded) {
  if (m.state !== 'ready') return '';
  const want = foldRepoID(m.repo_id);
  if (!(pinned || []).some((id) => foldRepoID(id) === want)) return '';
  return loaded ? 'pinned' : 'pinned, not loaded';
}

// size is bytes() for a figure that is genuinely a measurement: nothing pinned
// is "0 B", not the em dash bytes() shows for a size it does not know. This
// line is read before anything is ticked, so zero is its opening state.
function size(n) { return n ? bytes(n) : '0 B'; }

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
  // Shown wherever the panel is open, not only on the Statistics tab: on a
  // shared Mac the person whose requests are being recorded is not
  // necessarily the person who turned it on.
  $('recordingBadge').hidden = !(state.config && state.config.statistics);
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
  // The set the pool is actually enforcing, not the stored settings: those two
  // are the same except for the moment between a model arriving and the pin
  // for it being reconciled, and this surface is the one an operator would act
  // on. A pinned model that nothing has loaded is marked too — "pinned" is a
  // fact about the model, not about its memory.
  const pinned = state.pinned || [];

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
    const pinText = pinLabel(m, pinned, loaded);
    if (pinText) pill += `<span class="pill pinned">${pinText}</span>`;

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
  // From the machine object, not from the stored setting: what is enforced is
  // what the pool holds, and the stored setting is blank while the default is
  // in force.
  $('setBudget').value = budgetFieldValue(state.machine);
  updateBudgetHint();
  $('setHF').value   = c.hf_token || '';
  $('setStats').checked = !!c.statistics;
  $('setStatsMonths').value = c.stats_months;
  // Typed in megabytes and stored in bytes, which is how every other size in
  // this panel is shown.
  $('setStatsMB').value = Math.round((c.stats_max_bytes || 0) / (1024 * 1024));
  renderStatsStore();
  writeSampling('input', c.sampling);
  overrides = { ...(c.model_sampling || {}) };
  renderOverrides();
  renderMergeSwitches();
  renderPinSwitches();
}

// renderPinSwitches draws one box per downloaded model, and the figure that
// says what the ticked ones leave of the memory budget. The figure is what
// makes a pinned set that cannot fit visible while it is being chosen, rather
// than at the first request Gropius has to refuse.
function renderPinSwitches() {
  const box = $('pinList');
  const rows = pinRows(state.models || [], state.pinned || []);
  box.innerHTML = '';
  if (!rows.length) {
    box.innerHTML = '<p class="hint">Download a model and it appears here.</p>';
    updatePinBudget();
    return;
  }
  rows.forEach((r) => {
    const row = document.createElement('label');
    row.className = 'switch';
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.dataset.model = r.id;
    cb.checked = r.checked;
    cb.addEventListener('change', () => { settingsTouched = true; updatePinBudget(); });
    const name = document.createElement('span');
    name.textContent = r.absent ? `${r.id} (not on this Mac)` : r.id;
    row.appendChild(cb);
    row.appendChild(name);
    box.appendChild(row);
  });
  updatePinBudget();
}

// pinRows is the whole list of boxes the form draws: one per model on this
// Mac, then one per pin that names a model this Mac does not have.
//
// The second half is not a nicety. The form carries through every pin it does
// not list (see pinnedModels), so a pin with no box could never be removed —
// and a model deleted after it was pinned, or pinned before it was downloaded,
// leaves exactly that. Showing it is what makes every pin removable by the
// form that made it.
function pinRows(models, pinned) {
  const want = (pinned || []).map(foldRepoID);
  const have = new Set((models || []).map((m) => foldRepoID(m.repo_id)));
  const rows = (models || []).map((m) => ({
    id: m.repo_id,
    checked: want.includes(foldRepoID(m.repo_id)),
    absent: false,
  }));
  (pinned || []).filter((id) => !have.has(foldRepoID(id))).forEach((id) => {
    rows.push({ id, checked: true, absent: true });
  });
  return rows;
}

// pinBoxes are the boxes drawn above, one per model the form lists.
function pinBoxes() {
  return Array.from(document.querySelectorAll('#pinList input[type=checkbox]'));
}

// listedPinModels lists the models the form drew a box for.
function listedPinModels() {
  return pinBoxes().map((cb) => cb.dataset.model);
}

// checkedPinModels lists the models whose box is ticked.
function checkedPinModels() {
  return pinBoxes().filter((cb) => cb.checked).map((cb) => cb.dataset.model);
}

// pinnedModels returns the whole pinned list a save posts. The server replaces
// what it holds with this, so a model whose box is clear is simply left out —
// a list cannot be shortened by omission any other way. A pin for a model the
// form does not list is carried through, because a model can be pinned before
// it is downloaded and a form with no box for it has nothing to say about it.
function pinnedModels(current, listed, checked) {
  const shown = new Set((listed || []).map(foldRepoID));
  const out = (current || []).filter((id) => !shown.has(foldRepoID(id)));
  (checked || []).forEach((id) => out.push(id));
  return out;
}

// pinnedCharge is what the pinned models cost against the memory budget: each
// one's size plus a fifth, which is what the pool charges a loaded model
// (runtime.LoadCost). A model still downloading is charged the size it
// declares, because ticking its box now is a promise about the memory it will
// take when it lands. A pin naming a model this Mac does not have at all has
// no size to charge.
function pinnedCharge(models, pinned) {
  const want = new Set((pinned || []).map(foldRepoID));
  return (models || []).reduce((sum, m) => {
    if (!want.has(foldRepoID(m.repo_id))) return sum;
    const b = m.bytes || m.size_bytes || 0;
    return sum + b + Math.floor(b / 5);
  }, 0);
}

// budgetBytes is what the memory-budget field posts: the operator types
// gigabytes, the settings file stores bytes. A blank field — or anything that
// is not a positive number — is zero, which means the default share of this
// Mac's memory rather than a budget of nothing.
function budgetBytes(text) {
  const gb = parseFloat(text);
  if (!isFinite(gb) || gb <= 0) return 0;
  const bytes = Math.round(gb * 1024 * 1024 * 1024);
  // A figure no byte count can hold is not a budget. Left as Infinity it
  // serializes as null, which the server reads as "the field was not sent" and
  // answers by keeping what it had — a save that silently does nothing.
  if (!isFinite(bytes) || bytes > Number.MAX_SAFE_INTEGER) return 0;
  return bytes;
}

// budgetFieldValue is what the field shows: blank while the budget is the
// default, so that saving an unrelated setting does not quietly turn the
// default into a figure of the operator's own.
function budgetFieldValue(machine) {
  const m = machine || {};
  if (!m.budget || m.budget_is_default) return '';
  // Unrounded, so that what is shown posts back as the figure it came from, to
  // the byte: a budget set through the API is not always a round number of
  // gigabytes, and rounding it here would rewrite it on the next unrelated
  // save. Dividing by a power of two is exact, and JavaScript renders a number
  // as the shortest text that reads back as the same number, so the round trip
  // through budgetBytes is exact for any byte count a budget can hold. The
  // field is step="any" for the same reason: a stepped field refuses the whole
  // form for a value it does not land on, and the form holds every other
  // setting there is.
  return String(m.budget / (1024 * 1024 * 1024));
}

// budgetHint is the line beside the field: what the budget is, what share of
// this Mac that is, what the models in memory are using of it, and — above the
// warning threshold — that this memory is shared with everything else running.
//
// A Mac whose memory could not be read states the budget alone: a percentage
// of an unknown total is a number pretending to be information.
function budgetHint(machine) {
  const m = machine || {};
  const budget = m.budget || 0;
  const resident = m.resident_bytes || 0;
  const share = m.total_ram
    ? ` (${Math.round((budget * 100) / m.total_ram)}% of this Mac's ${size(m.total_ram)})`
    : ', because this Mac\'s memory could not be read';
  const parts = [`${m.budget_is_default ? 'The default budget is' : 'The budget is'} ${size(budget)}${share}.`];
  if (m.over_budget) {
    parts.push(`The models in memory use ${size(resident)}, over the budget: a lower budget applies to the next load, and nothing is unloaded on your behalf.`);
  } else if (resident) {
    parts.push(`The models in memory use ${size(resident)} of it.`);
  }
  if (m.warn_above && budget > m.warn_above) {
    parts.push('macOS and everything else running share this memory, and a model is charged the weights it loads rather than what a long conversation adds to it.');
  }
  return parts.join(' ');
}

// budgetShown is the machine as the figure being typed would leave it: what
// saving now would give. A cleared field is the default, so it shows the
// default rather than the figure it would replace — the panel documents
// clearing the field as the way back, and showing the old number there would
// make that instruction read as a lie.
function budgetShown(machine, typed) {
  const m = machine || {};
  const budget = typed || m.default_budget || m.budget || 0;
  return Object.assign({}, m, {
    budget,
    budget_is_default: !typed,
    over_budget: budget > 0 && (m.resident_bytes || 0) > budget,
  });
}

// updateBudgetHint writes that line, against the figure being typed rather
// than the one last saved, so the warning arrives while the number is being
// chosen instead of after it is stored.
function updateBudgetHint() {
  const line = $('budgetHint');
  if (!line) return;
  const shown = budgetShown(state.machine, budgetBytes($('setBudget').value));
  line.textContent = budgetHint(shown);
  line.className = shown.over_budget || (shown.warn_above && shown.budget > shown.warn_above) ? 'msg err' : 'hint';
}

// updatePinBudget writes the line beside the boxes: what the ticked models
// cost, and what that leaves for everything else.
function updatePinBudget() {
  const line = $('pinBudget');
  if (!line) return;
  const budget = (state.machine && state.machine.budget) || 0;
  const charge = pinnedCharge(state.models || [], checkedPinModels());
  if (!budget) {
    line.textContent = charge ? `Pinned models use about ${size(charge)}.` : '';
    line.className = 'hint';
    return;
  }
  const left = budget - charge;
  line.textContent = left >= 0
    ? `Pinned models use about ${size(charge)} of the ${size(budget)} memory budget, leaving ${size(left)} for everything else.`
    : `Pinned models use about ${size(charge)}, more than the ${size(budget)} memory budget — this cannot be saved.`;
  line.className = left >= 0 ? 'hint' : 'msg err';
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
$('setBudget').addEventListener('input', updateBudgetHint);

// Clear is not part of saving the form: it throws away what was recorded, so
// it happens when it is pressed and says what it did.
$('statsClear').addEventListener('click', async () => {
  if (!confirm('Remove every request record kept on this Mac? This cannot be undone. The recording switch is left as it is.')) return;
  try {
    await api('/api/stats/clear', { method: 'POST' });
    // Say so at once rather than at the next snapshot, so the button visibly
    // did something.
    if (state) state.stats_store = null;
    renderStatsStore();
    refreshStats();
  } catch (err) {
    $('settingsMsg').className = 'msg err';
    $('settingsMsg').textContent = err.message;
  }
});

// renderStatsStore says what is actually kept on disk, which is what makes the
// two figures above it mean something: a limit in megabytes says nothing about
// whether the store holds a week or a year.
function renderStatsStore() {
  const line = $('statsStoreLine');
  const store = state && state.stats_store;
  if (!store) {
    line.textContent = '';
    return;
  }
  if (store.refused) {
    line.textContent = 'Gropius could not open the store where records are kept, so the figures ' +
      'above are being held in memory only and nothing is on disk. Its own log says why.';
    return;
  }
  const parts = [];
  if (store.oldest) {
    parts.push(`Records from ${new Date(store.oldest * 1000).toLocaleDateString()} onwards`);
  } else {
    parts.push('No records kept yet');
  }
  parts.push(`${(store.bytes / (1024 * 1024)).toFixed(1)} MB in ${store.files} file${store.files === 1 ? '' : 's'}`);
  if (store.dropped) {
    parts.push(`${store.dropped} not written — the disk could not keep up`);
  }
  if (store.skipped) {
    parts.push(`${store.skipped} unreadable lines`);
  }
  line.textContent = `${parts.join(' · ')}.`;
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
    max_resident_bytes: budgetBytes($('setBudget').value),
    statistics:         $('setStats').checked,
    stats_months:       parseInt($('setStatsMonths').value, 10) || 6,
    stats_max_bytes:    (parseInt($('setStatsMB').value, 10) || 200) * 1024 * 1024,
    // A blank sampling field is sent as null, not as zero: the model server is
    // handed a flag only for a parameter that has a value.
    sampling:           readSampling('input'),
    model_sampling:     overrides,
    per_model:         perModelSettings(state.config.per_model, listedMergeModels(), checkedMergeModels()),
    pinned:            pinnedModels(state.pinned, listedPinModels(), checkedPinModels()),
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
    if (res.warning) parts.push(res.warning);
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
