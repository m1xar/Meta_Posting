// Dashboard: aggregate money and conversion metrics, what is live right now,
// and the connection health everything else depends on.
//
// The frame renders immediately; each panel owns its fetch and swaps its own
// skeleton for content, so one slow section never blanks the page.

import { api } from '../api.js';
import { el, int, money, relative, pill, dateTime } from '../format.js';
import { head, empty, metric, table, toast, skeleton, panelSkeleton, pager } from '../shell.js';
import { campaignTable, campaignActions, totalsMetrics } from './campaign_table.js';

const PAGE = 50;

export function overviewView() {
  const container = el('div', {});
  container.append(head('Dashboard', 'Workspace',
    el('button', {
      class: 'button', onclick: async (event) => {
        const button = event.currentTarget;
        button.disabled = true;
        button.textContent = 'Queueing…';
        try {
          const summary = await api.syncRefresh();
          toast(`Queued ${summary.jobs} sync job(s) for ${summary.accounts} account(s)`, 'ok');
        } catch (error) {
          toast(error.message, 'bad');
        } finally {
          button.disabled = false;
          button.textContent = 'Sync now';
        }
      },
    }, 'Sync now'),
    el('a', { class: 'button primary', href: '#/launcher' }, 'Launch campaigns')));

  const bannerBox = el('div', {});
  const metricsBox = el('div', { class: 'metrics' }, skeleton('5.4rem'));
  const liveBox = el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, 'Live campaigns'),
      el('a', { class: 'button small', href: '#/campaigns' }, 'All campaigns')),
    panelSkeleton(4));
  const connectionsBox = el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, 'Connected Meta accounts'),
      el('a', { class: 'button small', href: '/app/connect/meta' }, 'Connect another')),
    panelSkeleton(3));
  const accountsBox = el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, 'Ad accounts')),
    panelSkeleton(6));

  container.append(bannerBox, metricsBox, liveBox,
    el('div', { class: 'grid-2', style: 'align-items:start' }, connectionsBox, accountsBox));

  const fail = (box, error) => box.append(el('div', { class: 'error' }, error.message));

  // --- metrics + live campaigns (one fetch each) ---------------------------
  api.campaigns({ limit: 1 }).then(({ totals }) => {
    metricsBox.replaceChildren(...totalsMetrics(totals || {}, metric));
  }).catch((error) => { metricsBox.replaceChildren(); fail(metricsBox, error); });

  const refreshLive = () => api.campaigns({ status: 'live', limit: PAGE }).then((result) => {
    const views = result.campaigns || [];
    liveBox.querySelector('.label').textContent = `Live campaigns · ${result.total || 0}`;
    const body = campaignTable(views, { actions: (view) => campaignActions(view, refreshLive) })
      || el('p', { style: 'font-size:.86rem' }, 'Nothing is live right now.');
    while (liveBox.children.length > 1) liveBox.lastChild.remove();
    liveBox.append(body);
  }).catch((error) => { while (liveBox.children.length > 1) liveBox.lastChild.remove(); fail(liveBox, error); });
  refreshLive();

  // --- connections + the dead-token banner ---------------------------------
  api.connections({ limit: 50 }).then((connections) => {
    const items = connections.items || [];
    const broken = items.filter((c) => ['expired', 'error', 'revoked'].includes(c.status));
    if (broken.length) {
      bannerBox.replaceChildren(el('div', {
        class: 'error',
        style: 'display:flex;justify-content:space-between;align-items:center;gap:1rem;flex-wrap:wrap',
      },
        el('div', {},
          el('strong', {}, `Токен ${broken.map((c) => c.display_name || c.meta_user_id).join(', ')} протух — данные заморожены`),
          el('span', { style: 'display:block;margin-top:.25rem' },
            'Facebook отозвал доступ; спенд, статусы и кампании по этим кабинетам не обновляются. Переподключи аккаунт — воркер догонит пропущенные дни сам.')),
        el('a', { class: 'button primary', href: '/app/connect/meta' }, 'Переподключить')));
    }
    if (!items.length) {
      connectionsBox.replaceChildren(connectionsBox.firstChild,
        empty('No Meta account connected yet',
          'Connect a Meta account to start pulling ad accounts, campaigns and daily spend.',
          el('a', { class: 'button primary', href: '/app/connect/meta' }, 'Connect Meta')));
      return;
    }
    const rows = items.map((connection) => el('tr', {},
      el('td', { class: 'name' },
        el('span', {}, connection.display_name || connection.meta_user_id),
        el('span', { class: 'sub' }, connection.meta_user_id)),
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
        }, 'Sync'))));
    connectionsBox.replaceChildren(connectionsBox.firstChild,
      table([
        { label: 'Account' }, { label: 'Status' }, { label: 'Last sync' },
        { label: 'Token' }, { label: '', align: 'right' },
      ], rows));
  }).catch((error) => { connectionsBox.replaceChildren(connectionsBox.firstChild); fail(connectionsBox, error); });

  // --- ad accounts, server-paged -------------------------------------------
  const loadAccounts = (offset) => api.adAccounts({ limit: PAGE, offset }).then((accounts) => {
    accountsBox.querySelector('.label').textContent = `Ad accounts · ${accounts.total || 0}`;
    const rows = (accounts.items || []).map((account) => el('tr', {},
      el('td', { class: 'name' },
        el('a', { class: 'row-link', href: `#/account/${account.id}` }, account.name || account.meta_ad_account_id),
        el('span', { class: 'sub' }, `${account.account_id || ''} · ${account.currency || ''}`)),
      el('td', {}, account.account_status === 1
        ? pill('active', 'ok') : pill(`status ${account.account_status}`, 'warn')),
      el('td', { class: 'num' }, money((Number(account.amount_spent_minor) || 0) / 100, account.currency)),
      el('td', { class: 'num' }, money((Number(account.balance_minor) || 0) / 100, account.currency))));
    while (accountsBox.children.length > 1) accountsBox.lastChild.remove();
    accountsBox.append(
      table([
        { label: 'Account' }, { label: 'Status' },
        { label: 'Spent', align: 'right' }, { label: 'Balance', align: 'right' },
      ], rows) || el('p', {}, 'No ad accounts discovered yet.'),
      pager(accounts.total || 0, PAGE, offset, loadAccounts));
  }).catch((error) => { while (accountsBox.children.length > 1) accountsBox.lastChild.remove(); fail(accountsBox, error); });
  loadAccounts(0);

  return container;
}
