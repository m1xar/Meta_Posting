// Ad account: one ad account's aggregate stats and every campaign in it.
//
// Reached from the dashboard and campaign tables rather than the rail: the
// rail lists destinations, this is a drill-down with an ID in the route.

import { api } from '../api.js';
import { el, int, money, pill } from '../format.js';
import { head, empty, metric, skeleton, panelSkeleton } from '../shell.js';
import { campaignTable, campaignActions, totalsMetrics } from './campaign_table.js';

export function accountView(params) {
  const id = params && params[0];
  if (!id) return empty('No ad account selected', 'Open an ad account from the dashboard or a campaign row.');

  const container = el('div', {});
  container.append(head('Ad account', 'Loading…',
    el('a', { class: 'button small', href: '#/overview' }, 'Back')));
  const metricsBox = el('div', { class: 'metrics' }, skeleton('5.4rem'));
  const campaignsBox = el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, 'Campaigns'),
      el('a', { class: 'button small', href: '#/campaigns' }, 'All campaigns')),
    panelSkeleton(6));
  container.append(metricsBox, campaignsBox);

  api.accountStats(id).then((result) => fill(container, metricsBox, campaignsBox, result))
    .catch((error) => {
      metricsBox.replaceChildren();
      campaignsBox.append(el('div', { class: 'error' }, error.message));
    });
  return container;
}

function fill(container, metricsBox, campaignsBox, result) {
  const account = result.account || {};
  const totals = result.totals || {};
  const views = result.campaigns || [];

  const header = container.querySelector('.view-head');
  header.querySelector('h1').textContent = account.name || account.meta_ad_account_id || 'Ad account';
  header.querySelector('.eyebrow').textContent = [
    account.meta_ad_account_id, account.currency, account.timezone_name,
  ].filter(Boolean).join(' · ');

  metricsBox.replaceChildren(
    ...totalsMetrics(totals, metric),
    metric('Account status', account.account_status === 1 ? 'active' : `status ${account.account_status}`,
      account.disable_reason ? `disable reason ${account.disable_reason}` : null,
      account.account_status !== 1),
    metric('Spent lifetime', money((Number(account.amount_spent_minor) || 0) / 100, account.currency), 'reported by Meta'),
  );

  const refresh = () => window.dispatchEvent(new Event('route:refresh'));
  campaignsBox.querySelector('.label').textContent = `${views.length} campaign(s)`;
  while (campaignsBox.children.length > 1) campaignsBox.lastChild.remove();
  campaignsBox.append(views.length
    ? campaignTable(views, { actions: (view) => campaignActions(view, refresh) })
    : el('p', { style: 'font-size:.86rem' }, 'No campaigns in this account yet.'));
}
