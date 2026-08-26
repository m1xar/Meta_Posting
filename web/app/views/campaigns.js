// Campaigns: every campaign - launched here or discovered in the account -
// with metrics, checkpoint outcomes and live controls, server-paged by 50.

import { api } from '../api.js';
import { el, int, money } from '../format.js';
import { head, empty, metric, table, toast, skeleton, panelSkeleton, pager } from '../shell.js';
import {
  CAMPAIGN_COLUMNS, campaignRow, campaignActions, totalsMetrics,
} from './campaign_table.js';
import { checkpointEditor } from './guard_editor.js';

const PAGE = 50;
const state = { search: '', status: '', offset: 0 };

export function campaignsView() {
  const container = el('div', {});
  container.append(head('Campaigns', 'Everything running',
    el('a', { class: 'button primary', href: '#/launcher' }, 'Launch')));

  const metricsBox = el('div', { class: 'metrics' }, skeleton('5.4rem'));
  const listLabel = el('span', { class: 'label' }, 'Campaigns');
  const body = el('div', {}, panelSkeleton(8));
  const footer = el('div', {});
  const detail = el('div', {});

  let accountNames = new Map();
  let searchTimer = null;

  const search = el('input', {
    type: 'search', placeholder: 'Search by name or ID', value: state.search,
    style: 'max-width:16rem',
    oninput: (event) => {
      state.search = event.currentTarget.value;
      state.offset = 0;
      clearTimeout(searchTimer);
      searchTimer = setTimeout(load, 300);
    },
  });
  const status = el('select', {
    style: 'width:auto',
    onchange: (event) => { state.status = event.currentTarget.value; state.offset = 0; load(); },
  },
    el('option', { value: '' }, 'All statuses'),
    el('option', { value: 'live', selected: state.status === 'live' }, 'Live'),
    el('option', { value: 'paused', selected: state.status === 'paused' }, 'Paused'));

  container.append(metricsBox,
    el('section', { class: 'card panel' },
      el('header', {}, listLabel,
        el('div', { style: 'display:flex;gap:.6rem;align-items:center' }, status, search)),
      body, footer),
    detail);

  // Guards only attach to campaigns launched through this service; a
  // discovered campaign can still be paused and resumed.
  const actions = (view) => el('div', { style: 'display:flex;gap:.4rem;justify-content:flex-end' },
    campaignActions(view, load),
    view.campaign.source === 'launched'
      ? el('button', { class: 'button small', onclick: () => openEditor(view) }, 'Rules')
      : null,
    el('button', {
      class: 'button small',
      title: 'Duplicate this campaign in Meta (deep copy, paused)',
      onclick: async (event) => {
        if (!window.confirm('Duplicate this campaign in Meta? A deep copy (ad sets, ads, creatives) is created paused.')) return;
        const button = event.currentTarget;
        button.disabled = true;
        try {
          const result = await api.duplicateCampaign(view.campaign.id);
          toast('Duplicated → new campaign ' + result.new_meta_id + '. It will appear after sync.', 'ok');
        } catch (error) {
          toast(error.message, 'bad');
          button.disabled = false;
        }
      },
    }, 'Duplicate'),
  );

  async function load() {
    body.replaceChildren(panelSkeleton(8));
    footer.replaceChildren();
    try {
      const params = { limit: PAGE, offset: state.offset };
      if (state.search.trim()) params.search = state.search.trim();
      if (state.status) params.status = state.status;
      const [result, accounts] = await Promise.all([
        api.campaigns(params),
        accountNames.size ? Promise.resolve(null) : api.adAccounts({ limit: 500 }),
      ]);
      if (accounts) {
        accountNames = new Map((accounts.items || []).map((a) => [a.id, a.name || a.meta_ad_account_id]));
      }
      metricsBox.replaceChildren(...totalsMetrics(result.totals || {}, metric));
      const views = result.campaigns || [];
      listLabel.textContent = `${int(result.total || 0)} campaign(s)`;
      if (!views.length) {
        body.replaceChildren(state.search || state.status
          ? el('p', { style: 'font-size:.86rem' }, 'Nothing matches the filter.')
          : empty('Nothing here yet',
              'Campaigns launched through this service and campaigns discovered in the connected ad accounts both appear here.',
              el('a', { class: 'button primary', href: '#/launcher' }, 'Open the launcher')));
        return;
      }
      body.replaceChildren(table([...CAMPAIGN_COLUMNS, { label: '', align: 'right' }],
        views.map((view) => campaignRow(view, {
          accountName: accountNames.get(view.campaign.ad_account_id),
          actions,
        }))));
      footer.replaceChildren(pager(result.total || 0, PAGE, state.offset, (offset) => {
        state.offset = offset;
        load();
      }) || '');
    } catch (error) {
      body.replaceChildren(el('div', { class: 'error' }, error.message));
    }
  }

  // Editing the ladder on a live run. A campaign with its own guard edits
  // that guard; one riding the batch guard gets a personal guard instead, so
  // the change never silently rewrites the rules of its batch siblings.
  const openEditor = (view) => {
    const ownGuard = view.guard && view.guard.published_object_id === view.campaign.id;
    const editor = checkpointEditor((view.guard && view.guard.checkpoints) || []);
    const note = el('span', { class: 'muted', style: 'font-size:.84rem' });
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
              note.textContent = 'Add at least one checkpoint with a spend threshold.';
              return;
            }
            const button = event.currentTarget;
            button.disabled = true;
            try {
              if (ownGuard) await api.updateGuard(view.guard.id, { checkpoints });
              else await api.setCampaignGuard(view.campaign.id, { checkpoints });
              toast('Rules saved', 'ok');
              detail.replaceChildren();
              load();
            } catch (error) {
              note.textContent = error.message;
              button.disabled = false;
            }
          },
        }, ownGuard ? 'Save rules' : 'Create rules'),
        ownGuard && view.guard.status === 'active'
          ? el('button', {
              class: 'button small',
              onclick: async () => { await api.disableGuard(view.guard.id); toast('Guard disabled', 'ok'); load(); },
            }, 'Disable guard')
          : null,
        ownGuard && view.guard.status === 'disabled'
          ? el('button', {
              class: 'button small',
              onclick: async () => { await api.enableGuard(view.guard.id); toast('Guard enabled', 'ok'); load(); },
            }, 'Enable guard')
          : null,
        note),
    ));
    detail.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  };

  load();
  return container;
}
