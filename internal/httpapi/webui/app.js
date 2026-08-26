'use strict';
const csrf = () => decodeURIComponent((document.cookie.match(/(?:^|; )raze_csrf=([^;]*)/) || [])[1] || '');

async function api(path, options = {}) {
  const init = { method: options.method || 'GET', headers: {} };
  if (options.method && options.method !== 'GET') init.headers['X-CSRF-Token'] = csrf();
  if (options.body instanceof FormData) {
    init.body = options.body;
  } else if (options.body !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(options.body);
  }
  const response = await fetch(path, init);
  if (response.status === 401) { location = '/login'; throw new Error('unauthorized'); }
  let payload = null;
  try { payload = response.status === 204 ? null : await response.json(); } catch (_) {}
  if (!response.ok) throw new Error(payload?.error?.message || ('Request failed (' + response.status + ')'));
  return payload;
}

const esc = value => String(value ?? '');
const fmtMoney = value => '$' + (Number(value) || 0).toFixed(2);
const fmtNum = value => (Number(value) || 0).toLocaleString('en-US');
const fmtFloat = value => { const n = Number(value) || 0; return Number.isInteger(n) ? String(n) : n.toFixed(2); };

function statusPill(status) {
  const value = esc(status) || 'UNKNOWN';
  let kind = '';
  if (['ACTIVE', 'IN_PROCESS'].includes(value)) kind = ' live';
  else if (value.includes('PAUSED')) kind = ' paused';
  else if (['DISAPPROVED', 'WITH_ISSUES', 'DELETED', 'ARCHIVED'].includes(value)) kind = ' failed';
  return '<span class="pill' + kind + '">' + value + '</span>';
}

// Ladder state for a campaign: which checkpoint indexes passed/failed.
function checksSummary(view) {
  const checkpoints = view.guard ? (view.guard.checkpoints || []) : [];
  if (!checkpoints.length) return '<span class="muted">no guard</span>';
  const byIndex = {};
  (view.checks || []).forEach(check => { byIndex[check.checkpoint_index] = check.status; });
  return '<span class="checks">' + checkpoints.map((checkpoint, index) => {
    const status = byIndex[index] || 'pending';
    return '<span class="check ' + status + '" title="checkpoint at $' + fmtFloat(checkpoint.spend) + '">$' + fmtFloat(checkpoint.spend) + '</span>';
  }).join('') + '</span>';
}

function renderHeader(active, user) {
  const el = document.querySelector('header');
  el.innerHTML = '<a class="brand" href="/app">Raze <span>Posting</span></a>' +
    '<nav>' +
    ['<a href="/app"' + (active === 'dashboard' ? ' class="active"' : '') + '>Dashboard</a>',
     '<a href="/app/launch"' + (active === 'launch' ? ' class="active"' : '') + '>Launcher</a>',
     '<a href="/app/campaigns"' + (active === 'campaigns' ? ' class="active"' : '') + '>Campaigns</a>'].join('') +
    '</nav>' +
    '<div class="actions"><span class="muted" id="hdr-user"></span><button id="hdr-logout">Log out</button></div>';
  document.querySelector('#hdr-user').textContent = user?.login || '';
  document.querySelector('#hdr-logout').onclick = async () => {
    await fetch('/auth/logout', { method: 'POST', headers: { 'X-CSRF-Token': csrf() } });
    location = '/login';
  };
}

function campaignRow(view, options = {}) {
  const campaign = view.campaign;
  const insights = view.insights || {};
  const tracker = view.tracker || {};
  const spend = Number(insights.spend) || 0;
  const revenue = Number(tracker.revenue) || 0;
  const roi = spend > 0 ? (((revenue - spend) / spend) * 100).toFixed(0) + '%' : '—';
  const cells = [
    '<td><div>' + esc(campaign.name || campaign.meta_object_id) + '</div><div class="muted">' + esc(campaign.meta_object_id) + '</div></td>',
    '<td>' + statusPill(campaign.effective_status) + '</td>',
    '<td class="num">' + fmtMoney(spend) + '</td>',
    '<td class="num">' + fmtNum(insights.impressions) + '</td>',
    '<td class="num">' + fmtNum(insights.clicks) + '</td>',
    '<td class="num">' + fmtNum(tracker.clicks) + '</td>',
    '<td class="num">' + fmtFloat(tracker.leads) + '</td>',
    '<td class="num">' + fmtFloat(tracker.sales) + '</td>',
    '<td class="num">' + fmtMoney(revenue) + '</td>',
    '<td class="num">' + roi + '</td>',
    '<td>' + checksSummary(view) + '</td>',
  ];
  if (options.withActions) {
    const paused = String(campaign.effective_status || '').includes('PAUSED');
    cells.push('<td><div class="actions">' +
      '<button data-action="' + (paused ? 'resume' : 'pause') + '" data-id="' + campaign.id + '">' + (paused ? 'Resume' : 'Pause') + '</button>' +
      '<button data-action="guard" data-id="' + campaign.id + '">Rules</button>' +
      '</div></td>');
  }
  return '<tr>' + cells.join('') + '</tr>';
}

const CAMPAIGN_HEADERS = '<tr><th>Campaign</th><th>Status</th><th class="num">Spend</th><th class="num">Impr</th><th class="num">Clicks</th><th class="num">Trk clicks</th><th class="num">Regs</th><th class="num">Deps</th><th class="num">Revenue</th><th class="num">ROI</th><th>Guard</th>{ACTIONS}</tr>';

function campaignTable(views, options = {}) {
  const headers = CAMPAIGN_HEADERS.replace('{ACTIONS}', options.withActions ? '<th></th>' : '');
  if (!views.length) return '<div class="muted">No campaigns yet. Launch one from the Launcher.</div>';
  return '<div class="scroll"><table><thead>' + headers + '</thead><tbody>' +
    views.map(view => campaignRow(view, options)).join('') + '</tbody></table></div>';
}

function totalsCards(totals, extra = []) {
  const spend = Number(totals.spend) || 0;
  const revenue = Number(totals.tracker_revenue) || 0;
  const cards = [
    ['Spend', fmtMoney(spend)],
    ['Revenue', fmtMoney(revenue)],
    ['Profit', fmtMoney(revenue - spend)],
    ['Live', fmtNum(totals.live) + ' / ' + fmtNum(totals.campaigns)],
    ['Clicks', fmtNum(totals.clicks)],
    ['Regs', fmtFloat(totals.tracker_leads)],
    ['Deposits', fmtFloat(totals.tracker_sales)],
    ...extra,
  ];
  return cards.map(([label, value, sub]) =>
    '<div class="card"><div class="label">' + label + '</div><div class="value">' + value + '</div>' +
    (sub ? '<div class="sub">' + sub + '</div>' : '') + '</div>').join('');
}

// --- Checkpoint ladder editor -------------------------------------------------

const CP_FIELDS = [
  ['spend', 'Spend $'],
  ['min_clicks', 'Min clicks'],
  ['min_impressions', 'Min imprs'],
  ['min_tracker_clicks', 'Min trk clicks'],
  ['min_tracker_leads', 'Min regs'],
  ['min_tracker_sales', 'Min deps'],
  ['min_tracker_revenue', 'Min revenue $'],
];

function checkpointEditor(container, checkpoints) {
  container.innerHTML = '<div class="scroll"><table class="cp-table"><thead><tr>' +
    CP_FIELDS.map(([, label]) => '<th>' + label + '</th>').join('') + '<th></th></tr></thead><tbody></tbody></table></div>' +
    '<div class="actions" style="margin-top:10px"><button type="button" data-add>Add checkpoint</button>' +
    '<span class="muted">При достижении спенда проверяются минимумы; провал — кампания ставится на паузу.</span></div>';
  const body = container.querySelector('tbody');
  const addRow = values => {
    const tr = document.createElement('tr');
    tr.innerHTML = CP_FIELDS.map(([name]) =>
      '<td><input type="number" step="any" min="0" data-cp="' + name + '" value="' + (values?.[name] ?? '') + '"></td>').join('') +
      '<td><button type="button" data-remove>×</button></td>';
    tr.querySelector('[data-remove]').onclick = () => tr.remove();
    body.append(tr);
  };
  container.querySelector('[data-add]').onclick = () => addRow();
  (checkpoints && checkpoints.length ? checkpoints : [null]).forEach(addRow);
  return () => [...body.querySelectorAll('tr')].map(tr => {
    const checkpoint = {};
    tr.querySelectorAll('[data-cp]').forEach(input => {
      const value = Number(input.value);
      if (value > 0) checkpoint[input.dataset.cp] = input.dataset.cp.includes('leads') || input.dataset.cp.includes('sales') || input.dataset.cp.includes('revenue') || input.dataset.cp === 'spend' ? value : Math.round(value);
    });
    return checkpoint;
  }).filter(checkpoint => checkpoint.spend > 0);
}
