// Launcher: pick accounts, shape the campaign, set the stop conditions, go.
//
// Everything is on one page on purpose. A batch that publishes first and gets
// its spend guard afterwards is unguarded for exactly as long as it takes
// someone to remember, and that window is when a misconfigured launch does
// its damage.

import { api } from '../api.js';
import { el, int, money, pill } from '../format.js';
import { head, empty, table, toast } from '../shell.js';
import {
  OBJECTIVES, OPTIMIZATION_GOALS, BILLING_EVENTS, BID_STRATEGIES,
  CALL_TO_ACTIONS, SPECIAL_CATEGORIES, GUARD_METRICS, HIGHER_IS_WORSE, metricLabel,
} from './launcher_fields.js';

const BLOCKER_TEXT = {
  account_not_active: 'Not active on Meta',
  account_disabled: 'Disabled by Meta',
  no_funding_source: 'No payment method attached',
  missing_advertise_permission: 'No ADVERTISE permission',
  spend_cap_reached: 'Spend cap already reached',
  not_in_inventory: 'Not discovered yet',
};

const state = {
  selected: new Set(),
  showBlocked: false,
  guards: [],
  perAccount: false,
  mirror: false,
  source: null,
  media: null,
};

const field = (label, control, hint) =>
  el('label', { class: 'field' },
    el('span', {}, label),
    control,
    hint ? el('span', { class: 'muted', style: 'font-size:.74rem;font-weight:400;letter-spacing:0;text-transform:none' }, hint) : null,
  );

const options = (pairs, selected) =>
  pairs.map(([value, label]) => el('option', { value, selected: value === selected }, label));

// --- guards -----------------------------------------------------------------

function describeGuard(guard) {
  if (guard.kind === 'spend_cap') return `Pause once spend reaches ${guard.spend}`;
  const label = metricLabel(guard.metric);
  const worse = HIGHER_IS_WORSE.has(guard.metric);
  return worse
    ? `After ${guard.spend} spent, pause if ${label} is above ${guard.minimum}`
    : `After ${guard.spend} spent, pause unless ${label} reached ${guard.minimum}`;
}

function guardCard(guard, index, rerender, remove) {
  const kind = el('select', {}, ...options([
    ['spend_cap', 'Cap total spend'],
    ['spend_check', 'Check a result after spend'],
  ], guard.kind));
  const spend = el('input', { type: 'number', min: '1', step: '1', value: guard.spend });

  const metric = el('select', {},
    ...GUARD_METRICS.map(([group, items]) =>
      el('optgroup', { label: group }, ...options(items, guard.metric))));
  const minimum = el('input', { type: 'number', min: '0', step: 'any', value: guard.minimum });
  const level = el('select', {}, ...options([
    ['campaign', 'Pause the campaign'],
    ['adset', 'Pause the ad set'],
    ['ad', 'Pause the ad'],
  ], guard.level));
  const interval = el('select', {}, ...options([
    ['60', 'Every minute'],
    ['300', 'Every 5 minutes'],
    ['900', 'Every 15 minutes'],
    ['3600', 'Hourly'],
  ], String(guard.interval)));

  const summary = el('span', { class: 'pill info' }, describeGuard(guard));
  const check = el('div', { style: 'display:contents' });

  const sync = () => {
    guard.kind = kind.value;
    guard.spend = Number(spend.value) || 0;
    guard.metric = metric.value;
    guard.minimum = Number(minimum.value) || 0;
    guard.level = level.value;
    guard.interval = Number(interval.value);
    check.style.display = guard.kind === 'spend_check' ? 'contents' : 'none';
    summary.textContent = describeGuard(guard);
    rerender();
  };
  [kind, spend, metric, minimum, level, interval].forEach((n) => n.addEventListener('change', sync));

  check.append(
    field(HIGHER_IS_WORSE.has(guard.metric) ? 'Pause if above' : 'Requires at least', minimum),
    field('Metric', metric),
  );
  check.style.display = guard.kind === 'spend_check' ? 'contents' : 'none';

  return el('div', { class: 'card', style: 'padding:.9rem;display:grid;gap:.7rem' },
    el('div', { style: 'display:grid;grid-template-columns:repeat(auto-fit,minmax(8.5rem,1fr));gap:.7rem;align-items:end' },
      field(`Guard ${index + 1}`, kind),
      field('After spend', spend),
      check,
      field('Action', level),
      field('Check', interval),
      el('button', { class: 'button small danger', onclick: remove }, 'Remove'),
    ),
    summary,
  );
}

// --- view -------------------------------------------------------------------

export async function launcherView() {
  const [accounts, templates] = await Promise.all([
    api.launchAccounts(),
    api.launchTemplates({ limit: 300 }),
  ]);

  const container = el('div', {});
  const ready = (accounts.items || []).filter((a) => a.readiness.ready);
  const blocked = (accounts.items || []).filter((a) => !a.readiness.ready);
  const accountName = new Map((accounts.items || []).map((a) => [a.id, a.name || a.meta_ad_account_id]));

  container.append(head('Launcher', 'Publish with guards attached'));

  if (!ready.length) {
    container.append(empty('No ad account can be launched into',
      'An account needs to be active on Meta, free of a disable reason, have a payment method attached, and grant this user the ADVERTISE permission.'));
    return container;
  }

  // 1 · accounts -------------------------------------------------------------
  const accountBody = el('div', {});
  const summary = el('span', { class: 'muted', style: 'font-size:.82rem' });
  const renderSummary = () => {
    summary.textContent = `${state.selected.size} selected of ${ready.length} ready`
      + (blocked.length ? ` · ${blocked.length} blocked` : '');
  };
  const renderAccounts = () => {
    const rows = (state.showBlocked ? [...ready, ...blocked] : ready).map((account) => el('tr', {},
      el('td', {}, el('input', {
        type: 'checkbox', style: 'width:auto',
        ...(state.selected.has(account.id) ? { checked: true } : {}),
        ...(account.readiness.ready ? {} : { disabled: true }),
        onchange: (e) => {
          if (e.currentTarget.checked) state.selected.add(account.id);
          else state.selected.delete(account.id);
          renderSummary();
        },
      })),
      el('td', { class: 'name' },
        el('span', {}, account.name || account.meta_ad_account_id),
        el('span', { class: 'sub' }, `${account.account_id} · ${account.timezone_name || 'UTC'}`)),
      el('td', { class: 'num' }, money(account.amount_spent_minor / 100, account.currency)),
      el('td', { class: 'num' }, account.remaining_cap_minor < 0
        ? el('span', { class: 'muted' }, 'uncapped')
        : money(account.remaining_cap_minor / 100, account.currency)),
      el('td', {}, account.readiness.ready ? pill('ready', 'ok')
        : el('span', { style: 'display:flex;gap:.3rem;flex-wrap:wrap' },
            ...account.readiness.blockers.map((c) => pill(BLOCKER_TEXT[c] || c, 'bad')))),
    ));
    accountBody.replaceChildren(table([
      { label: '' }, { label: 'Ad account' },
      { label: 'Spent', align: 'right' }, { label: 'Cap left', align: 'right' },
      { label: 'Readiness' },
    ], rows) || el('p', {}, 'Nothing to show.'));
  };

  container.append(el('section', { class: 'card panel' },
    el('header', {},
      el('span', { class: 'label' }, '1 · Ad accounts'),
      el('div', { style: 'display:flex;gap:.6rem;align-items:center;flex-wrap:wrap' }, summary,
        el('button', { class: 'button small', onclick: () => { ready.forEach((a) => state.selected.add(a.id)); renderAccounts(); renderSummary(); } }, 'Select all ready'),
        el('button', { class: 'button small', onclick: () => { state.selected.clear(); renderAccounts(); renderSummary(); } }, 'Clear'),
        blocked.length ? el('button', {
          class: 'button small',
          onclick: (e) => {
            state.showBlocked = !state.showBlocked;
            e.currentTarget.textContent = state.showBlocked ? 'Hide blocked' : `Show ${blocked.length} blocked`;
            renderAccounts();
          },
        }, `Show ${blocked.length} blocked`) : null),
    ),
    accountBody,
  ));
  renderAccounts();
  renderSummary();

  // 2 · campaign -------------------------------------------------------------
  // Ad set names repeat heavily on real accounts - the same concept is
  // replicated across dozens of campaigns - so the picker has to carry
  // enough context to tell two identically named ad sets apart.
  const sourceSearch = el('input', { type: 'search', placeholder: 'Filter by name, account or ID' });
  const sourceSelect = el('select', { size: '6', style: 'min-height:9rem' });
  const sourceNote = el('div', { style: 'font-size:.82rem;margin-top:.6rem;display:flex;gap:.3rem;align-items:center;flex-wrap:wrap' });

  const renderSources = () => {
    const term = sourceSearch.value.trim().toLowerCase();
    const items = (templates.items || []).filter((t) => !term
      || (t.name || '').toLowerCase().includes(term)
      || (accountName.get(t.ad_account_id) || '').toLowerCase().includes(term)
      || t.meta_object_id.includes(term));
    sourceSelect.replaceChildren(...items.slice(0, 300).map((t) => el('option', {
      value: t.meta_object_id,
      selected: state.source && state.source.meta_object_id === t.meta_object_id,
    }, `${t.name || 'unnamed'} · ${accountName.get(t.ad_account_id) || '?'} · ${t.effective_status || ''} · ${t.meta_object_id.slice(-6)}`)));
    if (!state.source) sourceNote.textContent = `${items.length} of ${templates.total} ad sets — pick one to copy from`;
  };
  sourceSearch.addEventListener('input', renderSources);

  const campaignName = el('input', { type: 'text', placeholder: 'Spring prospecting' });
  const objective = el('select', {}, ...options(OBJECTIVES, 'OUTCOME_SALES'));
  const specialCategory = el('select', {},
    el('option', { value: '' }, 'None'), ...options(SPECIAL_CATEGORIES, ''));
  const bidStrategy = el('select', {}, ...options(BID_STRATEGIES, ''));
  const spendCap = el('input', { type: 'number', min: '0', step: 'any', placeholder: 'optional' });

  const adSetName = el('input', { type: 'text', placeholder: 'Ad set 1' });
  const dailyBudget = el('input', { type: 'number', min: '0', step: 'any', value: '1' });
  const lifetimeBudget = el('input', { type: 'number', min: '0', step: 'any', placeholder: 'optional' });
  const startTime = el('input', { type: 'datetime-local' });
  const endTime = el('input', { type: 'datetime-local' });
  const optimizationGoal = el('select', {}, ...options(OPTIMIZATION_GOALS, ''));
  const billingEvent = el('select', {}, ...options(BILLING_EVENTS, ''));

  const pageId = el('input', { type: 'text', placeholder: '1234567890' });
  const igActor = el('input', { type: 'text', placeholder: 'optional' });
  const link = el('input', { type: 'url', placeholder: 'https://example.com/offer' });
  const message = el('textarea', { style: 'min-height:5rem', placeholder: 'Primary text' });
  const headline = el('input', { type: 'text', placeholder: 'Headline' });
  const description = el('input', { type: 'text', placeholder: 'Description' });
  const cta = el('select', {}, ...options(CALL_TO_ACTIONS, 'LEARN_MORE'));
  const mediaInput = el('input', { type: 'file', accept: 'image/*,video/*', style: 'padding:.45rem' });
  const mediaNote = el('div', { style: 'font-size:.8rem;margin-top:.4rem' });
  const videoId = el('input', { type: 'text', placeholder: 'or paste a video ID already in the account' });

  mediaInput.addEventListener('change', async () => {
    const file = mediaInput.files && mediaInput.files[0];
    if (!file) return;
    state.media = null;
    mediaNote.replaceChildren(el('span', { class: 'muted' }, `Uploading ${file.name}...`));
    const connectionID = (accounts.items.find((a) => state.selected.has(a.id)) || accounts.items[0] || {}).connection_id;
    try {
      const uploaded = await api.uploadMedia(file, connectionID);
      state.media = uploaded;
      mediaNote.replaceChildren(
        el('span', { class: 'pill ok' }, `${uploaded.kind || 'media'} ready`),
        el('span', { class: 'muted', style: 'margin-left:.4rem' },
          `${file.name} — uploaded into each selected ad account at launch`),
      );
    } catch (error) {
      state.media = null;
      mediaNote.replaceChildren(el('span', { class: 'pill bad' }, error.message));
    }
  });
  const urlTags = el('input', { type: 'text', placeholder: 'utm_source=meta&utm_campaign=...' });

  sourceSelect.addEventListener('change', () => {
    const template = (templates.items || []).find((t) => t.meta_object_id === sourceSelect.value);
    if (!template) return;
    state.source = template;
    // Fill every field the source can answer. Showing "Inherit from source"
    // in a dropdown while the real value stays hidden is the same as not
    // answering: the point of copying an ad set is to see what you copied
    // and change what you want.
    const raw = template.raw || {};
    if (!adSetName.value) adSetName.value = `${template.name || 'Ad set'} copy`;
    if (template.objective) objective.value = template.objective;

    const goal = template.optimization_goal || raw.optimization_goal;
    if (goal && [...optimizationGoal.options].some((o) => o.value === goal)) optimizationGoal.value = goal;
    const billing = template.billing_event || raw.billing_event;
    if (billing && [...billingEvent.options].some((o) => o.value === billing)) billingEvent.value = billing;
    if (template.daily_budget_minor) dailyBudget.value = String(template.daily_budget_minor / 100);
    if (raw.bid_strategy && [...bidStrategy.options].some((o) => o.value === raw.bid_strategy)) {
      bidStrategy.value = raw.bid_strategy;
    }

    // The creative comes from one of the ad set's own ads: the page, the
    // Instagram identity and the copy are what someone actually wants
    // carried over, and a page ID typed from memory is how a launch ends up
    // published by the wrong brand.
    const creative = template.creative || {};
    const story = creative.object_story_spec || {};
    // A flexible ad keeps its copy in asset_feed_spec instead, as arrays of
    // variants; the first of each is the representative one.
    const feed = creative.asset_feed_spec || {};
    const firstOf = (list, key) => (Array.isArray(list) && list.length ? list[0][key] : undefined);

    if (story.page_id) pageId.value = story.page_id;
    if (story.instagram_user_id) igActor.value = story.instagram_user_id;
    if (creative.url_tags) urlTags.value = creative.url_tags;

    const body = story.link_data || story.video_data || {};
    const pick = (...values) => values.find((v) => v !== undefined && v !== null && v !== '');

    const linkValue = pick(body.link, firstOf(feed.link_urls, 'website_url'));
    if (linkValue) link.value = linkValue;
    const messageValue = pick(body.message, firstOf(feed.bodies, 'text'));
    if (messageValue) message.value = messageValue;
    const headlineValue = pick(body.name, body.title, firstOf(feed.titles, 'text'));
    if (headlineValue) headline.value = headlineValue;
    const descriptionValue = pick(body.description, body.link_description, firstOf(feed.descriptions, 'text'));
    if (descriptionValue) description.value = descriptionValue;

    const ctaValue = pick(
      body.call_to_action && body.call_to_action.type,
      Array.isArray(feed.call_to_action_types) ? feed.call_to_action_types[0] : undefined,
    );
    if (ctaValue && [...cta.options].some((o) => o.value === ctaValue)) cta.value = ctaValue;

    const sourceVideo = pick(
      story.video_data && story.video_data.video_id,
      firstOf(feed.videos, 'video_id'),
    );
    if (sourceVideo) videoId.value = sourceVideo;
    // Show what was actually inherited. "Inheriting targeting" is a claim;
    // the countries and the pixel ID are the evidence, and they are what
    // someone would otherwise only discover in the preview.
    const countries = ((raw.targeting || {}).geo_locations || {}).countries || [];
    const pixel = (raw.promoted_object || {}).pixel_id;
    sourceNote.replaceChildren(
      el('span', {}, `From "${template.name || template.meta_object_id}" in ${accountName.get(template.ad_account_id) || 'unknown account'} — `),
      el('span', { class: 'pill info' }, countries.length ? `geo ${countries.join(', ')}` : 'no geo set'),
      pixel ? el('span', { class: 'pill info', style: 'margin-left:.3rem' }, `pixel ${pixel}`) : null,
      template.optimization_goal
        ? el('span', { class: 'pill info', style: 'margin-left:.3rem' }, template.optimization_goal)
        : null,
    );
  });

  container.append(el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, '2 · Campaign')),

    el('div', { class: 'stack', style: 'margin-bottom:1.4rem' },
      el('span', { class: 'label' }, 'Copy settings from an existing ad set'),
      el('p', { style: 'font-size:.84rem' },
        'Pick one below. Its targeting, promoted object (pixel) and attribution are copied into the new ad set. Names repeat across accounts, so each row shows the account and the last digits of the ID.'),
      sourceSearch, sourceSelect, sourceNote),

    el('span', { class: 'label' }, 'New campaign'),
    el('div', { class: 'grid-2', style: 'margin:.6rem 0 1.2rem' },
      field('Campaign name', campaignName, 'The name of the campaign being created'),
      field('Objective', objective),
      field('Special ad category', specialCategory, 'Required by Meta for credit, employment, housing, politics and gambling'),
      field('Bid strategy', bidStrategy),
      field('Campaign spend cap', spendCap, 'Hard ceiling enforced by Meta itself'),
    ),

    el('span', { class: 'label' }, 'New ad set'),
    el('p', { style: 'font-size:.82rem;margin:.4rem 0 0' },
      'Targeting and the pixel come from the ad set chosen above. These fields cover what differs.'),
    el('div', { class: 'grid-2', style: 'margin:.6rem 0 1.2rem' },
      field('Ad set name', adSetName, 'The name of the ad set being created'),
      field('Daily budget', dailyBudget, 'Set either daily or lifetime, not both'),
      field('Lifetime budget', lifetimeBudget),
      field('Optimization goal', optimizationGoal),
      field('Billing event', billingEvent),
      field('Start', startTime),
      field('End', endTime, 'Required with a lifetime budget'),
    ),

    el('span', { class: 'label' }, 'New creative'),
    el('div', { class: 'grid-2', style: 'margin:.6rem 0' },
      field('Page ID', pageId, 'The Page that publishes the ad'),
      field('Instagram account ID', igActor),
      field('Destination URL', link),
      field('Call to action', cta),
      field('Headline', headline),
      field('Description', description),
      field('Image or video', el('div', {}, mediaInput, mediaNote),
        'Uploaded once here, then published into every selected ad account'),
      field('Existing video ID', videoId, 'Only if the video is already in the account'),
      field('URL tags', urlTags),
    ),
    field('Primary text', message),
  ));

  // 3 · guards ---------------------------------------------------------------
  const guardBody = el('div', { class: 'stack' });
  const renderGuards = () => {
    guardBody.replaceChildren(...state.guards.map((guard, index) =>
      guardCard(guard, index, () => {}, () => { state.guards.splice(index, 1); renderGuards(); })));
    if (!state.guards.length) {
      guardBody.append(el('p', { style: 'font-size:.84rem' },
        'No stop condition. The campaign will run until something else stops it.'));
    }
  };
  const scopeToggle = el('input', { type: 'checkbox', style: 'width:auto', onchange: (e) => { state.perAccount = e.currentTarget.checked; } });
  const mirrorToggle = el('input', { type: 'checkbox', style: 'width:auto;margin-top:.2rem', onchange: (e) => { state.mirror = e.currentTarget.checked; } });

  container.append(el('section', { class: 'card panel' },
    el('header', {},
      el('span', { class: 'label' }, '3 · Stop conditions'),
      el('button', {
        class: 'button small',
        onclick: () => {
          state.guards.push({ kind: 'spend_cap', spend: 1, metric: 'impressions', minimum: 100, level: 'campaign', interval: 60 });
          renderGuards();
        },
      }, 'Add guard'),
    ),
    el('p', { style: 'margin-bottom:.8rem;font-size:.84rem' },
      'Guards can only pause, never resume or raise a budget. A minute-level guard also keeps its insights collected every minute.'),
    guardBody,
    el('label', { style: 'display:flex;gap:.55rem;align-items:center;margin-top:1rem;font-size:.86rem' },
      scopeToggle, el('span', {}, 'Apply each guard per ad account instead of once across the batch')),
    el('label', { style: 'display:flex;gap:.55rem;align-items:start;margin-top:.6rem;font-size:.86rem' },
      mirrorToggle, el('span', {}, 'Also register each guard inside Meta as a backstop. ',
        el('span', { class: 'muted' }, 'Slower, but keeps working if this service is down. Guards Meta cannot express are left unmirrored rather than approximated.'))),
  ));
  renderGuards();

  // 4 · launch ---------------------------------------------------------------
  const status = el('div', {});
  const previewBox = el('div', {});

  const buildPayload = () => {
    const selected = [...state.selected];
    const guards = state.guards.map((guard) => ({
      scope_level: guard.level,
      evaluation_interval_seconds: guard.interval,
      guard: guard.kind === 'spend_cap'
        ? { kind: 'spend_cap', spend: guard.spend }
        : { kind: 'spend_check', spend: guard.spend, metric: guard.metric, minimum: guard.minimum },
    }));
    const localTime = (value) => (value ? `${value}:00+0000`.replace('T', 'T') : '');
    const payload = {
      connection_id: (accounts.items.find((a) => a.id === selected[0]) || {}).connection_id,
      name: campaignName.value.trim(),
      idempotency_key: `launch-${Date.now()}`,
      ad_account_ids: selected,
      mirror_to_meta: state.mirror,
      form: {
        source_ad_set: state.source ? state.source.raw : undefined,
        campaign: {
          name: campaignName.value.trim(),
          objective: objective.value,
          special_ad_categories: specialCategory.value ? [specialCategory.value] : [],
          bid_strategy: bidStrategy.value || undefined,
          spend_cap: Number(spendCap.value) || 0,
        },
        ad_set: {
          name: adSetName.value.trim(),
          daily_budget: Number(dailyBudget.value) || 0,
          lifetime_budget: Number(lifetimeBudget.value) || 0,
          start_time: localTime(startTime.value),
          end_time: localTime(endTime.value),
          optimization_goal: optimizationGoal.value || undefined,
          billing_event: billingEvent.value || undefined,
        },
        creative: {
          page_id: pageId.value.trim(),
          instagram_actor_id: igActor.value.trim(),
          link: link.value.trim(),
          message: message.value,
          headline: headline.value,
          description: description.value,
          call_to_action_type: cta.value,
          video_id: videoId.value.trim(),
          // A video arriving through a binding has no ID yet; the creative
          // still has to be shaped as a video so the binding has somewhere
          // to write each account's own video ID.
          use_video: !!(state.media && state.media.kind === 'video'),
          url_tags: urlTags.value.trim(),
        },
      },
    };
    if (state.media && state.media.id) {
      // A binding rather than a hash: image hashes are per ad account, so the
      // publisher uploads this file into each target and writes that
      // account's own hash into the creative.
      payload.media_bindings = [{
        media_id: state.media.id,
        target: state.media.kind === 'video'
          ? '/creative/object_story_spec/video_data/video_id'
          : '/creative/object_story_spec/link_data/image_hash',
      }];
    }
    if (state.perAccount) payload.account_rules = Object.fromEntries(selected.map((id) => [id, guards]));
    else payload.shared_rules = guards;
    return payload;
  };

  const showError = (error) => {
    status.replaceChildren(el('div', { class: 'error' },
      el('strong', {}, error.code || 'Rejected'), el('span', {}, error.message)));
  };

  const guardSelection = () => {
    if (!state.selected.size) {
      status.replaceChildren(el('div', { class: 'error' }, 'Select at least one ready ad account.'));
      return false;
    }
    if (!state.source) {
      // Without a source the ad set carries no targeting and no pixel. Meta
      // accepts that and then delivers to nobody, which looks like a broken
      // launch rather than a missing choice.
      status.replaceChildren(el('div', { class: 'error' },
        el('strong', {}, 'No source ad set chosen'),
        el('span', {}, 'Targeting and the pixel are copied from an existing ad set. Pick one in step 2.')));
      return false;
    }
    return true;
  };

  container.append(el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, '4 · Review and launch')),
    el('p', { style: 'margin-bottom:.9rem;font-size:.84rem' },
      'Preview composes the request locally and costs nothing. Dry run sends it to Meta with validate_only, so Meta checks it without creating anything.'),
    status,
    previewBox,
    el('div', { style: 'display:flex;gap:.6rem;flex-wrap:wrap' },
      el('button', {
        class: 'button',
        onclick: async (event) => {
          if (!guardSelection()) return;
          const button = event.currentTarget;
          button.disabled = true;
          status.replaceChildren();
          try {
            const result = await api.launchPreview(buildPayload());
            previewBox.replaceChildren(el('div', { class: 'card panel' },
              el('span', { class: 'label' }, `Would publish into ${result.ad_accounts} account(s)`),
              ...(result.guards || []).map((text) => el('div', { class: 'pill info', style: 'margin:.3rem .3rem 0 0' }, text)),
              el('pre', { class: 'token-reveal', style: 'margin-top:.7rem;white-space:pre-wrap;max-height:22rem;overflow:auto' },
                JSON.stringify(result.hierarchy, null, 2)),
            ));
          } catch (error) { showError(error); } finally { button.disabled = false; }
        },
      }, 'Preview'),
      el('button', {
        class: 'button',
        onclick: async (event) => {
          if (!guardSelection()) return;
          const button = event.currentTarget;
          button.disabled = true;
          status.replaceChildren();
          try {
            // validate_only asks Meta to check the request without creating
            // anything, so a rejection costs nothing but a round trip.
            const payload = { ...buildPayload(), validate_only: true, leave_paused: true };
            const result = await api.launch(payload);
            toast('Dry run accepted by Meta', 'ok');
            window.location.hash = `#/history`;
            void result;
          } catch (error) { showError(error); } finally { button.disabled = false; }
        },
      }, 'Dry run'),
      el('button', {
        class: 'button primary',
        onclick: async (event) => {
          if (!guardSelection()) return;
          if (!window.confirm(`Publish into ${state.selected.size} ad account(s) with ${state.guards.length} guard(s)?`)) return;
          const button = event.currentTarget;
          button.disabled = true;
          status.replaceChildren();
          try {
            const result = await api.launch(buildPayload());
            if (result.warning) {
              status.replaceChildren(el('div', { class: 'error' },
                el('strong', {}, 'Published, but not fully guarded'), el('span', {}, result.warning)));
            }
            toast(`Launched into ${state.selected.size} account(s)`, 'ok');
            window.location.hash = `#/history`;
          } catch (error) { showError(error); } finally { button.disabled = false; }
        },
      }, 'Launch'),
    ),
  ));

  renderSources();
  return container;
}
