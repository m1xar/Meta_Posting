// Shared campaign presentation: the metrics row and the checkpoint ladder.
//
// Dashboard, Campaigns and the ad-account page all show the same joined row -
// Facebook lifetime numbers next to the tracker's registrations and deposits -
// so the row is built in one place and the pages differ only in scope.

import { api } from '../api.js';
import { el, int, money, pill, campaignStatusPill } from '../format.js';
import { table, toast } from '../shell.js';

export const isLive = (status) => ['ACTIVE', 'IN_PROCESS', 'WITH_ISSUES'].includes(status);
export const isPaused = (status) => String(status || '').includes('PAUSED');

const num = (value) => {
  const parsed = Number(value) || 0;
  return Number.isInteger(parsed) ? int(parsed) : parsed.toFixed(2);
};

/** The ladder as chips: one per checkpoint, colored by its recorded check. */
export function ladderChips(view) {
  const checkpoints = (view.guard && view.guard.checkpoints) || [];
  if (!checkpoints.length) return el('span', { class: 'muted', style: 'font-size:.78rem' }, 'no guard');
  const byIndex = {};
  (view.checks || []).forEach((check) => { byIndex[check.checkpoint_index] = check.status; });
  const toneFor = { passed: 'ok', failed: 'bad', overridden: 'warn' };
  return el('span', { style: 'display:flex;gap:.25rem;flex-wrap:wrap' },
    ...checkpoints.map((checkpoint, index) => {
      const status = byIndex[index];
      return el('span', {
        class: `pill ${toneFor[status] || 'info'}`,
        title: checkpointTitle(checkpoint, status),
      }, `$${num(checkpoint.spend)}`);
    }));
}

function checkpointTitle(checkpoint, status) {
  const parts = [];
  if (checkpoint.min_clicks) parts.push(`clicks ≥ ${checkpoint.min_clicks}`);
  if (checkpoint.min_impressions) parts.push(`impressions ≥ ${checkpoint.min_impressions}`);
  if (checkpoint.min_tracker_clicks) parts.push(`tracker clicks ≥ ${checkpoint.min_tracker_clicks}`);
  if (checkpoint.min_tracker_leads) parts.push(`regs ≥ ${checkpoint.min_tracker_leads}`);
  if (checkpoint.min_tracker_sales) parts.push(`deposits ≥ ${checkpoint.min_tracker_sales}`);
  if (checkpoint.min_tracker_revenue) parts.push(`revenue ≥ ${checkpoint.min_tracker_revenue}`);
  return `at $${num(checkpoint.spend)}: ${parts.join(', ') || 'no thresholds'}${status ? ` — ${status}` : ' — not reached yet'}`;
}

export const CAMPAIGN_COLUMNS = [
  { label: 'Campaign' }, { label: 'Status' },
  { label: 'Spend', align: 'right' }, { label: 'Impr', align: 'right' },
  { label: 'Clicks', align: 'right' }, { label: 'Trk clicks', align: 'right' },
  { label: 'Regs', align: 'right' }, { label: 'Deps', align: 'right' },
  { label: 'Revenue', align: 'right' }, { label: 'ROI', align: 'right' },
  { label: 'Guard' },
];

/** One campaign row. options: { accountName, actions: fn(view) -> node } */
export function campaignRow(view, options = {}) {
  const campaign = view.campaign;
  const insights = view.insights || {};
  const tracker = view.tracker || {};
  const spend = Number(insights.spend) || 0;
  const revenue = Number(tracker.revenue) || 0;
  const roi = spend > 0 ? `${Math.round(((revenue - spend) / spend) * 100)}%` : '—';
  const cells = [
    el('td', { class: 'name' },
      el('span', {}, campaign.name || campaign.meta_object_id),
      el('span', { class: 'sub' }, [
        options.accountName, campaign.meta_object_id,
        campaign.source === 'discovered' ? 'discovered' : null,
      ].filter(Boolean).join(' · '))),
    el('td', {}, campaignStatusPill(campaign.effective_status)),
    el('td', { class: 'num' }, money(spend)),
    el('td', { class: 'num' }, int(insights.impressions)),
    el('td', { class: 'num' }, int(insights.clicks)),
    el('td', { class: 'num' }, int(tracker.clicks)),
    el('td', { class: 'num' }, num(tracker.leads)),
    el('td', { class: 'num' }, num(tracker.sales)),
    el('td', { class: 'num' }, money(revenue)),
    el('td', { class: 'num' }, roi),
    el('td', {}, ladderChips(view)),
  ];
  if (options.actions) cells.push(el('td', { class: 'num' }, options.actions(view)));
  // A campaign with spend but no tracker match is flagged: its revenue is
  // unknown, so any profit read on it is misleading until tracking is fixed.
  const untracked = !view.tracker && (Number(insights.spend) || 0) > 0;
  return el('tr', untracked ? { class: 'row-untracked', title: 'Нет данных Keitaro — проверь разметку ссылки (sub_id_7)' } : {}, ...cells);
}

export function campaignTable(views, options = {}) {
  const columns = options.actions ? [...CAMPAIGN_COLUMNS, { label: '', align: 'right' }] : CAMPAIGN_COLUMNS;
  return table(columns, views.map((view) => campaignRow(view, {
    accountName: options.accountNames ? options.accountNames.get(view.campaign.ad_account_id) : undefined,
    actions: options.actions,
  })));
}

/** Pause/resume buttons wired to the campaign endpoints. */
export function campaignActions(view, onChanged) {
  const paused = isPaused(view.campaign.effective_status);
  const toggle = el('button', {
    class: `button small${paused ? ' primary' : ''}`,
    onclick: async (event) => {
      const button = event.currentTarget;
      button.disabled = true;
      try {
        if (paused) await api.resumeCampaign(view.campaign.id);
        else await api.pauseCampaign(view.campaign.id);
        toast(paused ? 'Campaign resumed' : 'Campaign paused', 'ok');
        onChanged();
      } catch (error) {
        toast(error.message, 'bad');
        button.disabled = false;
      }
    },
  }, paused ? 'Resume' : 'Pause');
  return toggle;
}

export function totalsMetrics(totals, metric) {
  const spend = Number(totals.spend) || 0;
  const revenue = Number(totals.tracker_revenue) || 0;
  const trackedSpend = Number(totals.tracked_spend) || 0;
  const untrackedSpend = Number(totals.untracked_spend) || 0;
  const trackedProfit = revenue - trackedSpend;
  const roi = trackedSpend > 0 ? `${Math.round((trackedProfit / trackedSpend) * 100)}%` : '—';
  const coverage = totals.campaigns
    ? `${int(totals.matched)}/${int(totals.campaigns)} с трекером`
    : '';
  return [
    // Tracked half: revenue is comparable to the spend that produced it.
    metric('Tracked spend', money(trackedSpend), coverage),
    metric('Revenue', money(revenue), 'from Keitaro'),
    metric('Profit (tracked)', money(trackedProfit), `ROI ${roi}`, trackedProfit < 0),
    // Untracked half: spend with no tracker data, shown apart so it does not
    // masquerade as a loss.
    metric('Untracked spend', money(untrackedSpend), 'нет данных Keitaro', untrackedSpend > 0),
    metric('Live', `${int(totals.live)} / ${int(totals.campaigns)}`, `${int(totals.paused)} paused`),
    metric('Regs', num(totals.tracker_leads)),
    metric('Deposits', num(totals.tracker_sales)),
  ];
}
