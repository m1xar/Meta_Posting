// Campaigns: every launched campaign with its statuses, checkpoint outcomes
// and live controls - pause, resume, and editing the ladder mid-flight.

import { api } from '../api.js';
import { el, int, money } from '../format.js';
import { head, empty, metric, table, toast } from '../shell.js';
import {
  CAMPAIGN_COLUMNS, campaignRow, campaignActions, totalsMetrics, isLive, isPaused,
} from './campaign_table.js';
import { checkpointEditor } from './guard_editor.js';

const filters = { mode: '', term: '' };

export async function campaignsView() {
  const [result, accounts] = await Promise.all([
    api.campaigns(),
    api.adAccounts({ limit: 500 }),
  ]);
  const views = result.campaigns || [];
  const totals = result.totals || {};
  const accountNames = new Map((accounts.items || []).map((a) => [a.id, a.name || a.meta_ad_account_id]));

  const container = el('div', {});
  container.append(head('Campaigns', 'Everything launched',
    el('a', { class: 'button primary', href: '#/launcher' }, 'Launch')));

  container.append(el('div', { class: 'metrics' }, ...totalsMetrics(totals, metric)));

  if (!views.length) {
    container.append(empty('Nothing launched yet',
      'Campaigns published through the launcher appear here with their Facebook and tracker metrics and every checkpoint outcome.',
      el('a', { class: 'button primary', href: '#/launcher' }, 'Open the launcher')));
    return container;
  }

  const refresh = () => window.dispatchEvent(new Event('route:refresh'));

  const search = el('input', {
    type: 'search', placeholder: 'Filter by name or ID', value: filters.term,
    style: 'max-width:16rem',
    oninput: (event) => { filters.term = event.currentTarget.value; renderRows(); },
  });
  const mode = el('select', {
    style: 'width:auto',
    onchange: (event) => { filters.mode = event.currentTarget.value; renderRows(); },
  },
    el('option', { value: '' }, 'All statuses'),
    el('option', { value: 'live', selected: filters.mode === 'live' }, 'Live'),
    el('option', { value: 'paused', selected: filters.mode === 'paused' }, 'Paused'));

  const body = el('div', {});
  const detail = el('div', {});

  const filtered = () => views.filter((view) => {
    const status = view.campaign.effective_status || '';
    if (filters.mode === 'live' && !isLive(status)) return false;
    if (filters.mode === 'paused' && !isPaused(status)) return false;
    const term = filters.term.trim().toLowerCase();
    if (!term) return true;
    return `${view.campaign.name} ${view.campaign.meta_object_id}`.toLowerCase().includes(term);
  });

  // Guards only attach to campaigns launched through this service; a
  // discovered campaign can still be paused and resumed.
  const actions = (view) => el('div', { style: 'display:flex;gap:.4rem;justify-content:flex-end' },
    campaignActions(view, refresh),
    view.campaign.source === 'launched'
      ? el('button', { class: 'button small', onclick: () => openEditor(view) }, 'Rules')
      : null,
  );

  const renderRows = () => {
    const rows = filtered().map((view) => campaignRow(view, {
      accountName: accountNames.get(view.campaign.ad_account_id),
      actions,
    }));
    body.replaceChildren(
      table([...CAMPAIGN_COLUMNS, { label: '', align: 'right' }], rows)
        || el('p', { style: 'font-size:.86rem' }, 'Nothing matches the filter.'),
    );
  };

  // Editing the ladder on a live run. A campaign with its own guard edits
  // that guard; one riding the batch guard gets a personal guard instead, so
  // the change never silently rewrites the rules of its batch siblings.
  const openEditor = (view) => {
    const ownGuard = view.guard && view.guard.published_object_id === view.campaign.id;
    const editor = checkpointEditor((view.guard && view.guard.checkpoints) || []);
    const status = el('span', { class: 'muted', style: 'font-size:.84rem' });
    detail.replaceChildren(el('section', { class: 'card panel' },
      el('header', {},
        el('span', { class: 'label' }, `Checkpoint ladder · ${view.campaign.name || view.campaign.meta_object_id}`),
        el('button', { class: 'button small', onclick: () => detail.replaceChildren() }, 'Close')),
      el('p', { style: 'font-size:.84rem;margin-bottom:.8rem' }, ownGuard
        ? 'This campaign has its own rules; saving updates them in place.'
        : view.guard
          ? 'This campaign currently follows its batch guard. Saving creates personal rules for this campaign only.'
          : 'No guard applies yet. Saving creates personal rules for this campaign.'),
      editor.node,
      el('div', { style: 'display:flex;gap:.6rem;align-items:center;margin-top:1rem' },
        el('button', {
          class: 'button primary',
          onclick: async (event) => {
            const checkpoints = editor.read();
            if (!checkpoints.length) {
              status.textContent = 'Add at least one checkpoint with a spend threshold.';
              return;
            }
            const button = event.currentTarget;
            button.disabled = true;
            try {
              if (ownGuard) await api.updateGuard(view.guard.id, { checkpoints });
              else await api.setCampaignGuard(view.campaign.id, { checkpoints });
              toast('Rules saved', 'ok');
              refresh();
            } catch (error) {
              status.textContent = error.message;
              button.disabled = false;
            }
          },
        }, ownGuard ? 'Save rules' : 'Create rules'),
        ownGuard && view.guard.status === 'active'
          ? el('button', {
              class: 'button small',
              onclick: async () => { await api.disableGuard(view.guard.id); toast('Guard disabled', 'ok'); refresh(); },
            }, 'Disable guard')
          : null,
        ownGuard && view.guard.status === 'disabled'
          ? el('button', {
              class: 'button small',
              onclick: async () => { await api.enableGuard(view.guard.id); toast('Guard enabled', 'ok'); refresh(); },
            }, 'Enable guard')
          : null,
        status),
    ));
    detail.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  };

  container.append(el('section', { class: 'card panel' },
    el('header', {},
      el('span', { class: 'label' }, `${views.length} campaign(s)`),
      el('div', { style: 'display:flex;gap:.6rem;align-items:center' }, mode, search)),
    body,
  ));
  container.append(detail);
  renderRows();
  return container;
}
