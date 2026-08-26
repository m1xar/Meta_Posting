// Overview: is everything connected, syncing, and spending as expected.

import { api } from '../api.js';
import { el, int, money, relative, pill, dateTime, isoDaysAgo } from '../format.js';
import { head, empty, metric, table, toast } from '../shell.js';

export async function overviewView() {
  const [connections, accounts, insights, jobs] = await Promise.all([
    api.connections({ limit: 50 }),
    api.adAccounts({ limit: 200 }),
    api.dailyInsights({ level: 'account', since: isoDaysAgo(6), limit: 500 }),
    api.jobs({ limit: 100 }),
  ]);

  const container = el('div', {});
  container.append(head('Overview', 'Workspace'));

  const rollup = insights.rollup || {};
  const activeConnections = (connections.items || []).filter((c) => c.status === 'active').length;
  const failing = (jobs.items || []).filter((j) => j.status === 'dead').length;

  container.append(el('div', { class: 'metrics' },
    metric('Connections', int(activeConnections), `${connections.total || 0} total`),
    metric('Ad accounts', int(accounts.total || 0)),
    metric('Spend, 7d', money(rollup.spend, rollup.currency || 'USD'), `${rollup.days || 0} day(s) with data`),
    metric('Clicks, 7d', int(rollup.clicks)),
    metric('Failed jobs', int(failing), failing ? 'Check History' : 'Nothing stuck', failing === 0),
  ));

  if (!(connections.items || []).length) {
    container.append(empty(
      'No Meta account connected yet',
      'Connect a Meta account to start pulling ad accounts, campaign inventory and daily spend. You will be redirected to Meta and back.',
      el('a', { class: 'button primary', href: '/app/connect/meta' }, 'Connect Meta'),
    ));
    return container;
  }

  const rows = (connections.items || []).map((connection) => el('tr', {},
    el('td', { class: 'name' },
      el('span', {}, connection.display_name || connection.meta_user_id),
      el('span', { class: 'sub' }, connection.meta_user_id),
    ),
    el('td', {}, pill(connection.status)),
    el('td', { class: 'numeric' }, relative(connection.last_synced_at)),
    el('td', { class: 'numeric' }, connection.token_expires_at ? dateTime(connection.token_expires_at) : 'long-lived'),
    el('td', { class: 'num' },
      el('button', {
        class: 'button small',
        onclick: async (event) => {
          const button = event.currentTarget;
          button.disabled = true;
          try {
            await api.syncConnection(connection.id);
            toast('Sync queued', 'ok');
          } catch (error) {
            toast(error.message, 'bad');
          } finally {
            button.disabled = false;
          }
        },
      }, 'Sync'),
    ),
  ));

  container.append(el('section', { class: 'card panel' },
    el('header', {},
      el('span', { class: 'label' }, 'Connected Meta accounts'),
      el('a', { class: 'button small', href: '/app/connect/meta' }, 'Connect another'),
    ),
    table([
      { label: 'Account' }, { label: 'Status' }, { label: 'Last sync' },
      { label: 'Token' }, { label: '', align: 'right' },
    ], rows),
  ));

  // Ad accounts that are disabled upstream will never produce data, and that
  // is a Meta-side fact rather than something to debug here.
  const disabled = (accounts.items || []).filter((account) => account.account_status !== 1);
  if (disabled.length) {
    container.append(el('section', { class: 'card panel' },
      el('header', {}, el('span', { class: 'label' }, 'Ad accounts not serving')),
      el('p', { style: 'font-size:.86rem' },
        `${disabled.length} of ${accounts.total} ad accounts are not in an active state on Meta's side, so they will not report delivery.`),
      table([{ label: 'Account' }, { label: 'Status', align: 'right' }],
        disabled.slice(0, 10).map((account) => el('tr', {},
          el('td', { class: 'name' },
            el('span', {}, account.name || account.meta_ad_account_id),
            el('span', { class: 'sub' }, account.timezone_name || ''),
          ),
          el('td', { class: 'num' }, pill(`status ${account.account_status}`, 'warn')),
        ))),
    ));
  }
  return container;
}
