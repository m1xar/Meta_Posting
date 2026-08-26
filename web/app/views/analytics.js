// Analytics: daily spend and delivery, per level.

import { api } from '../api.js';
import { el, int, money, ratio, date, orUnknown } from '../format.js';
import { head, empty, metric, table, moreRow } from '../shell.js';

const LEVELS = ['account', 'campaign', 'adset', 'ad'];

const filters = {
  level: 'campaign',
  ad_account_id: '',
  meta_object_id: '',
  limit: 250,
  since: '',
  until: '',
};

function objectLabel(row) {
  return el('td', { class: 'name' },
    el('span', {}, row.object_name || row.meta_object_id),
    el('span', { class: 'sub' }, row.meta_object_id),
  );
}

/** Route: #/analytics or #/analytics/<level>/<metaObjectId>, which is where
 *  the Inventory "Spend" links land. */
export async function analyticsView(params = []) {
  const [level, objectId] = params;
  if (level && objectId) {
    filters.level = level;
    filters.meta_object_id = objectId;
  } else {
    // Reaching /analytics without an object means "everything": the rail
    // link must not silently inherit a filter from a previous drill-down.
    filters.meta_object_id = '';
  }

  const [accounts, page] = await Promise.all([
    api.adAccounts({ limit: 250 }),
    api.dailyInsights({ ...filters }),
  ]);

  const rollup = page.rollup || {};
  const currency = rollup.currency || 'USD';

  const container = el('div', {});
  container.append(head(
    'Analytics',
    filters.meta_object_id ? `${filters.level} ${filters.meta_object_id}` : 'Delivery by day',
    filters.meta_object_id
      ? el('button', {
          class: 'button',
          onclick: () => {
            filters.meta_object_id = '';
            window.location.hash = '#/analytics';
            window.dispatchEvent(new CustomEvent('route:refresh'));
          },
        }, 'Clear object filter')
      : null,
  ));

  // Reach and frequency are deduplicated per query window, so the server
  // omits them for any window wider than a day. Showing them as absent, with
  // the reason, beats showing a plausible wrong number.
  const multiDay = (rollup.days || 0) > 1 || rollup.reach === undefined;

  container.append(el('div', { class: 'metrics' },
    metric('Spend', money(rollup.spend, currency), `${rollup.days || 0} day(s), ${rollup.rows || 0} rows`),
    metric('Impressions', int(rollup.impressions)),
    metric('Clicks', int(rollup.clicks), `CTR ${ratio(rollup.ctr)}`),
    metric('CPC', money(rollup.cpc, currency), `CPM ${money(rollup.cpm, currency)}`),
    metric(
      'Reach',
      orUnknown(rollup.reach, int),
      multiDay ? 'Not additive across days' : 'Deduplicated by Meta',
      multiDay,
    ),
  ));

  const form = el('form', { class: 'card filters', onsubmit: (event) => event.preventDefault() });

  const levelSelect = el('select', {},
    ...LEVELS.map((level) => el('option', { value: level, selected: level === filters.level }, level)));
  const accountSelect = el('select', {},
    el('option', { value: '' }, 'All ad accounts'),
    ...(accounts.items || []).map((account) => el('option', {
      value: account.id, selected: account.id === filters.ad_account_id,
    }, account.name || account.meta_ad_account_id)));
  const sinceInput = el('input', { type: 'date', value: filters.since });
  const untilInput = el('input', { type: 'date', value: filters.until });

  form.append(
    el('label', { class: 'field' }, el('span', {}, 'Level'), levelSelect),
    el('label', { class: 'field' }, el('span', {}, 'Ad account'), accountSelect),
    el('label', { class: 'field' }, el('span', {}, 'Since'), sinceInput),
    el('label', { class: 'field' }, el('span', {}, 'Until'), untilInput),
    el('button', {
      class: 'button primary',
      onclick: () => {
        filters.level = levelSelect.value;
        filters.ad_account_id = accountSelect.value;
        filters.since = sinceInput.value;
        filters.until = untilInput.value;
        filters.limit = 250;
        // Changing the level by hand means the caller wants the whole level,
        // not the single object they arrived from.
        if (levelSelect.value !== filters.level) filters.meta_object_id = '';
        window.dispatchEvent(new CustomEvent('route:refresh'));
      },
    }, 'Apply'),
  );
  container.append(form);

  const rows = (page.items || []).map((row) => el('tr', {},
    el('td', { class: 'numeric' }, date(row.date)),
    objectLabel(row),
    el('td', { class: 'num' }, money(row.spend, row.currency || currency)),
    el('td', { class: 'num' }, int(row.impressions)),
    el('td', { class: 'num' }, int(row.clicks)),
    el('td', { class: 'num' }, ratio(row.ctr)),
    el('td', { class: 'num' }, money(row.cpc, row.currency || currency)),
    el('td', { class: 'num' }, money(row.cpm, row.currency || currency)),
  ));

  const body = table([
    { label: 'Date' }, { label: 'Object' },
    { label: 'Spend', align: 'right' }, { label: 'Impr.', align: 'right' },
    { label: 'Clicks', align: 'right' }, { label: 'CTR', align: 'right' },
    { label: 'CPC', align: 'right' }, { label: 'CPM', align: 'right' },
  ], rows);

  container.append(body || empty(
    'No delivery in this window',
    'The accounts were polled and Meta returned no rows, which means nothing ran on these days rather than that data is missing. Rows appear once an ad account spends.',
  ));

  container.append(moreRow((page.items || []).length, page.total || 0, () => {
    filters.limit += 250;
    window.dispatchEvent(new CustomEvent('route:refresh'));
  }));
  return container;
}
