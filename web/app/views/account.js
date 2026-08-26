// Ad account: one ad account's aggregate stats and every campaign in it.
//
// Reached from the dashboard and campaign tables rather than the rail: the
// rail lists destinations, this is a drill-down with an ID in the route.

import { api } from '../api.js';
import { el, int, money, pill } from '../format.js';
import { head, empty, metric } from '../shell.js';
import { campaignTable, campaignActions, totalsMetrics } from './campaign_table.js';

export async function accountView(params) {
  const id = params && params[0];
  if (!id) return empty('No ad account selected', 'Open an ad account from the dashboard or a campaign row.');

  const result = await api.accountStats(id);
  const account = result.account || {};
  const totals = result.totals || {};
  const views = result.campaigns || [];

  const container = el('div', {});
  const subtitle = [
    account.meta_ad_account_id, account.currency, account.timezone_name,
  ].filter(Boolean).join(' · ');
  container.append(head(account.name || account.meta_ad_account_id || 'Ad account', subtitle,
    el('a', { class: 'button small', href: '#/overview' }, 'Back')));

  container.append(el('div', { class: 'metrics' },
    ...totalsMetrics(totals, metric),
    metric('Account status', account.account_status === 1 ? 'active' : `status ${account.account_status}`,
      account.disable_reason ? `disable reason ${account.disable_reason}` : null,
      account.account_status !== 1),
    metric('Spent lifetime', money((Number(account.amount_spent_minor) || 0) / 100, account.currency), 'reported by Meta'),
  ));

  const refresh = () => window.dispatchEvent(new Event('route:refresh'));
  if (!views.length) {
    container.append(empty('No campaigns in this account yet',
      'Campaigns launched into this ad account will appear here with tracker metrics and checkpoint outcomes.',
      el('a', { class: 'button primary', href: '#/launcher' }, 'Open the launcher')));
    return container;
  }

  container.append(el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, `${views.length} campaign(s)`),
      el('a', { class: 'button small', href: '#/campaigns' }, 'All campaigns')),
    campaignTable(views, { actions: (view) => campaignActions(view, refresh) }),
  ));
  return container;
}
