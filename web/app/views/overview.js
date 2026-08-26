// Dashboard: aggregate money and conversion metrics, what is live right now,
// and the connection health that everything else depends on.

import { api } from '../api.js';
import { el, int, money, relative, pill, dateTime } from '../format.js';
import { head, empty, metric, table, toast } from '../shell.js';
import { campaignTable, campaignActions, totalsMetrics, isLive } from './campaign_table.js';

export async function overviewView() {
  const [connections, accounts, campaignsResult, jobs] = await Promise.all([
    api.connections({ limit: 50 }),
    api.adAccounts({ limit: 500 }),
    api.campaigns(),
    api.jobs({ limit: 100 }),
  ]);

  const container = el('div', {});
  container.append(head('Dashboard', 'Workspace',
    el('a', { class: 'button primary', href: '#/launcher' }, 'Launch campaigns')));

  const totals = campaignsResult.totals || {};
  const views = campaignsResult.campaigns || [];
  const accountNames = new Map((accounts.items || []).map((a) => [a.id, a.name || a.meta_ad_account_id]));
  const failing = (jobs.items || []).filter((j) => j.status === 'dead').length;

  container.append(el('div', { class: 'metrics' },
    ...totalsMetrics(totals, metric),
    metric('Failed jobs', int(failing), failing ? 'Check the worker' : 'Nothing stuck', failing !== 0),
  ));

  if (!(connections.items || []).length) {
    container.append(empty(
      'No Meta account connected yet',
      'Connect a Meta account to start pulling ad accounts, campaign inventory and daily spend. You will be redirected to Meta and back.',
      el('a', { class: 'button primary', href: '/app/connect/meta' }, 'Connect Meta'),
    ));
    return container;
  }

  // Live campaigns first: the dashboard's job is "what is spending right now".
  const refresh = () => window.dispatchEvent(new Event('route:refresh'));
  const live = views.filter((view) => isLive(view.campaign.effective_status));
  container.append(el('section', { class: 'card panel' },
    el('header', {},
      el('span', { class: 'label' }, `Live campaigns · ${live.length}`),
      el('a', { class: 'button small', href: '#/campaigns' }, 'All campaigns')),
    live.length
      ? campaignTable(live, { accountNames, actions: (view) => campaignActions(view, refresh) })
      : el('p', { style: 'font-size:.86rem' }, 'Nothing is live right now.'),
  ));

  const connectionRows = (connections.items || []).map((connection) => el('tr', {},
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

  const accountRows = (accounts.items || []).map((account) => el('tr', {},
    el('td', { class: 'name' },
      el('a', { class: 'row-link', href: `#/account/${account.id}` }, account.name || account.meta_ad_account_id),
      el('span', { class: 'sub' }, `${account.account_id || ''} · ${account.currency || ''}`)),
    el('td', {}, account.account_status === 1
      ? pill('active', 'ok') : pill(`status ${account.account_status}`, 'warn')),
    el('td', { class: 'num' }, money((Number(account.amount_spent_minor) || 0) / 100, account.currency)),
    el('td', { class: 'num' }, money((Number(account.balance_minor) || 0) / 100, account.currency)),
  ));

  container.append(el('div', { class: 'grid-2', style: 'align-items:start' },
    el('section', { class: 'card panel' },
      el('header', {},
        el('span', { class: 'label' }, 'Connected Meta accounts'),
        el('a', { class: 'button small', href: '/app/connect/meta' }, 'Connect another')),
      table([
        { label: 'Account' }, { label: 'Status' }, { label: 'Last sync' },
        { label: 'Token' }, { label: '', align: 'right' },
      ], connectionRows)),
    el('section', { class: 'card panel' },
      el('header', {}, el('span', { class: 'label' }, `Ad accounts · ${accounts.total || 0}`)),
      table([
        { label: 'Account' }, { label: 'Status' },
        { label: 'Spent', align: 'right' }, { label: 'Balance', align: 'right' },
      ], accountRows.slice(0, 30)) || el('p', {}, 'No ad accounts discovered yet.')),
  ));

  return container;
}
