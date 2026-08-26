// Admin: the only place that reads across tenants. Every request here is
// recorded as an admin.cross_tenant_read audit event on the server.

import { api } from '../api.js';
import { el, int, money, relative, pill, dateTime, isoDaysAgo } from '../format.js';
import { head, empty, metric, table } from '../shell.js';

export async function adminView() {
  const [users, connections, accounts, insights, limits] = await Promise.all([
    api.adminUsers({ limit: 200 }),
    api.adminConnections({ limit: 200 }),
    api.adminAdAccounts({ limit: 200 }),
    api.adminDailyInsights({ level: 'account', since: isoDaysAgo(6), limit: 500 }),
    api.adminRateLimits(),
  ]);

  const container = el('div', {});
  container.append(head('All tenants', 'Administration'));

  container.append(el('div', { class: 'card panel' },
    el('span', { class: 'label' }, 'Cross-tenant view'),
    el('p', { style: 'margin-top:.5rem;font-size:.86rem' },
      'These reads span every tenant and are recorded in the audit log. Your own dashboard elsewhere in this workspace stays scoped to your data.'),
  ));

  const rollup = insights.rollup || {};
  container.append(el('div', { class: 'metrics' },
    metric('Users', int(users.total || 0)),
    metric('Connections', int(connections.total || 0)),
    metric('Ad accounts', int(accounts.total || 0)),
    metric('Spend, 7d', money(rollup.spend, rollup.currency || 'USD')),
    metric('Throttled', int((limits.items || []).filter((s) => s.throttled_until).length),
      'Ad accounts Meta is limiting'),
  ));

  container.append(el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, 'Users')),
    table([{ label: 'User' }, { label: 'Email' }, { label: 'Role' }, { label: 'Last login' }],
      (users.items || []).map((user) => el('tr', {},
        el('td', {}, user.username),
        el('td', { class: 'mono', style: 'font-size:.8rem' }, user.email),
        el('td', {}, pill(user.role)),
        el('td', { class: 'numeric' }, user.last_login_at ? relative(user.last_login_at) : 'never'),
      ))),
  ));

  container.append(el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, 'Connections across tenants')),
    table([{ label: 'Meta account' }, { label: 'Status' }, { label: 'Last sync' }],
      (connections.items || []).map((connection) => el('tr', {},
        el('td', { class: 'name' },
          el('span', {}, connection.display_name || connection.meta_user_id),
          el('span', { class: 'sub' }, connection.meta_user_id),
        ),
        el('td', {}, pill(connection.status)),
        el('td', { class: 'numeric' }, relative(connection.last_synced_at)),
      ))) || el('p', {}, 'No connections.'),
  ));

  // Rate-limit pressure is read from Meta's own usage headers, so it is
  // visible before a throttle turns into a stalled backfill.
  const limitRows = (limits.items || []).slice(0, 40).map((sync) => el('tr', {},
    el('td', { class: 'mono', style: 'font-size:.78rem' }, sync.ad_account_id),
    el('td', {}, sync.throttled_until
      ? pill(`until ${dateTime(sync.throttled_until)}`, 'bad')
      : pill('clear', 'ok')),
    el('td', { class: 'num' }, int(sync.consecutive_failures || 0)),
    el('td', { class: 'muted', style: 'font-size:.78rem' }, sync.last_error || ''),
  ));

  container.append(el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, 'Meta rate limits')),
    table([{ label: 'Ad account' }, { label: 'Throttle' }, { label: 'Failures', align: 'right' }, { label: 'Last error' }],
      limitRows) || empty('No usage recorded yet',
        'Usage appears once the workers have made Graph requests for an ad account.'),
  ));

  return container;
}
