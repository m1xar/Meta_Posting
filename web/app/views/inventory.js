// Inventory: every campaign, ad set and ad in the connected ad accounts,
// including objects created in Ads Manager rather than here.
//
// A real profile holds ~1,900 objects, so a flat list per level is not
// usable: you cannot tell which campaign an ad set belongs to. This view
// drills - campaign, then its ad sets, then their ads - and every row links
// through to the spend for that object.

import { api } from '../api.js';
import { el, int, relative, pill, money } from '../format.js';
import { head, empty, metric, table, moreRow } from '../shell.js';

const LEVELS = ['campaign', 'adset', 'ad'];
const CHILD = { campaign: 'adset', adset: 'ad', ad: null };

const PAGE = 250;

const state = { adAccountId: '', includeGone: false, search: '', limit: PAGE };

/** Route: #/inventory or #/inventory/<level>/<metaObjectId>/<label...> */
function parseRoute(params) {
  const [level, objectId, ...label] = params;
  if (!level || !LEVELS.includes(level) || !objectId) return null;
  return { level, objectId, label: decodeURIComponent(label.join('/') || objectId) };
}

async function levelCounts(scope) {
  const counts = await Promise.all(LEVELS.map((level) =>
    api.adEntities({ ...scope, level, limit: 1 }).then((page) => page.total || 0)));
  return Object.fromEntries(LEVELS.map((level, index) => [level, counts[index]]));
}

export async function inventoryView(params) {
  const drill = parseRoute(params);
  const [accounts, counts] = await Promise.all([
    api.adAccounts({ limit: 250 }),
    levelCounts({ ad_account_id: state.adAccountId }),
  ]);

  const container = el('div', {});

  // The level being listed: the child of whatever we drilled into.
  const level = drill ? CHILD[drill.level] : 'campaign';
  const scope = {
    level,
    ad_account_id: state.adAccountId,
    include_gone: state.includeGone ? 'true' : '',
    limit: state.limit,
  };
  if (drill && drill.level === 'campaign') scope.campaign_id = drill.objectId;
  if (drill && drill.level === 'adset') scope.adset_id = drill.objectId;

  container.append(head(
    drill ? drill.label : 'Inventory',
    drill ? `${drill.level} · ${level}s inside it` : 'Objects in your ad accounts',
    drill ? el('a', { class: 'button', href: '#/inventory' }, 'All campaigns') : null,
  ));

  if (!drill) {
    // Counts first: 107 campaigns behind 924 ad sets behind 844 ads is the
    // shape of the account, and it is invisible in a paginated list.
    container.append(el('div', { class: 'metrics' },
      ...LEVELS.map((name) => metric(`${name}s`, int(counts[name]))),
      metric('Ad accounts', int(accounts.total || 0)),
    ));
  }

  const accountSelect = el('select', {},
    el('option', { value: '' }, 'All ad accounts'),
    ...(accounts.items || []).map((account) => el('option', {
      value: account.id, selected: account.id === state.adAccountId,
    }, account.name || account.meta_ad_account_id)));
  const searchInput = el('input', { type: 'search', placeholder: 'Name or ID', value: state.search });
  const goneInput = el('input', {
    type: 'checkbox', style: 'width:auto', ...(state.includeGone ? { checked: true } : {}),
  });

  container.append(el('form', { class: 'card filters', onsubmit: (e) => e.preventDefault() },
    el('label', { class: 'field' }, el('span', {}, 'Ad account'), accountSelect),
    el('label', { class: 'field' }, el('span', {}, 'Search'), searchInput),
    el('label', { class: 'field' }, el('span', {}, 'Include removed'), goneInput),
    el('button', {
      class: 'button primary',
      onclick: () => {
        state.adAccountId = accountSelect.value;
        state.search = searchInput.value.trim();
        state.includeGone = goneInput.checked;
        state.limit = PAGE;
        window.dispatchEvent(new CustomEvent('route:refresh'));
      },
    }, 'Apply'),
  ));

  const page = await api.adEntities(scope);
  const term = state.search.toLowerCase();
  const items = (page.items || []).filter((entity) => !term
    || (entity.name || '').toLowerCase().includes(term)
    || entity.meta_object_id.includes(term));

  const childLevel = CHILD[level];
  const rows = items.map((entity) => {
    const label = encodeURIComponent(entity.name || entity.meta_object_id);
    return el('tr', {},
      el('td', { class: 'name' },
        childLevel
          ? el('a', {
              href: `#/inventory/${level}/${entity.meta_object_id}/${label}`,
              style: 'text-decoration:underline;text-underline-offset:3px',
            }, entity.name || entity.meta_object_id)
          : el('span', {}, entity.name || entity.meta_object_id),
        el('span', { class: 'sub' },
          entity.meta_object_id + (entity.objective ? ` · ${entity.objective}` : '')),
      ),
      el('td', {}, pill(entity.effective_status || entity.status || 'unknown')),
      // Provenance: an object this platform published can be traced back to
      // its batch, one from Ads Manager cannot.
      el('td', {}, entity.is_owned
        ? pill('published here', 'info')
        : el('span', { class: 'muted', style: 'font-size:.78rem' }, 'external')),
      el('td', { class: 'num' },
        entity.daily_budget_minor
          ? money(entity.daily_budget_minor / 100)
          : entity.lifetime_budget_minor
            ? money(entity.lifetime_budget_minor / 100) + ' lt'
            : '—'),
      el('td', { class: 'numeric' }, relative(entity.last_seen_at)),
      el('td', { class: 'num' },
        el('div', { style: 'display:flex;gap:.4rem;justify-content:flex-end' },
          el('a', {
            class: 'button small',
            href: `#/analytics/${level}/${entity.meta_object_id}`,
            title: 'Spend for this object',
          }, 'Spend'),
          entity.disappeared_at ? pill('removed', 'bad') : null,
        )),
    );
  });

  container.append(table([
    { label: childLevel ? `${level} (opens ${childLevel}s)` : level },
    { label: 'Status' }, { label: 'Origin' },
    { label: 'Budget', align: 'right' }, { label: 'Last seen' },
    { label: '', align: 'right' },
  ], rows) || empty(
    drill ? `No ${level}s under this ${drill.level}` : 'Nothing in the inventory yet',
    drill
      ? 'The sweep found no children here. Objects deleted in Ads Manager keep their history but stop being listed unless you include removed ones.'
      : 'The inventory sweep runs every six hours and records every campaign, ad set and ad in your connected ad accounts.',
  ));

  if (items.length) {
    if (state.search) {
      container.append(el('p', { class: 'muted', style: 'margin-top:.8rem;font-size:.8rem' },
        `${items.length} of ${int(page.total)} ${level}(s) match "${state.search}", within the ${state.limit} loaded.`));
    } else {
      container.append(moreRow(items.length, page.total || 0, () => {
        state.limit += PAGE;
        window.dispatchEvent(new CustomEvent('route:refresh'));
      }));
    }
  }
  return container;
}
