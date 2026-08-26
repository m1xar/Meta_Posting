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
  CALL_TO_ACTIONS, SPECIAL_CATEGORIES,
} from './launcher_fields.js';
import { checkpointEditor } from './guard_editor.js';

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

// --- view -------------------------------------------------------------------

export async function launcherView() {
  const accounts = await api.launchAccounts();
  // Source ad sets are loaded per selected account, not up front.
  let templates = { items: [], total: 0 };

  const container = el('div', {});
  // Reassigned once the source/post loaders exist; called on every change of
  // the selected accounts so existing options track the selection.
  let onSelectionChange = () => {};
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
          onSelectionChange();
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
        el('button', { class: 'button small', onclick: () => { ready.forEach((a) => state.selected.add(a.id)); renderAccounts(); renderSummary(); onSelectionChange(); } }, 'Select all ready'),
        el('button', { class: 'button small', onclick: () => { state.selected.clear(); renderAccounts(); renderSummary(); onSelectionChange(); } }, 'Clear'),
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
  const sourceSearch = el('input', { type: 'search', placeholder: 'Search ad sets by name or ID (server-side)' });
  const sourceSelect = el('select', { size: '6', style: 'min-height:9rem' });
  const sourceNote = el('div', { style: 'font-size:.82rem;margin-top:.6rem;display:flex;gap:.3rem;align-items:center;flex-wrap:wrap' });

  // Source ad sets are offered only from the accounts selected for this
  // launch: copying an ad set into an account it does not belong to drags in a
  // pixel that account cannot use, which Meta rejects.
  const renderSources = () => {
    const items = templates.items || [];
    sourceSelect.replaceChildren(...items.map((t) => el('option', {
      value: t.id,
      selected: state.source && state.source.id === t.id,
    }, `${t.name || 'unnamed'} · ${accountName.get(t.ad_account_id) || '?'} · ${t.effective_status || ''} · ${t.meta_object_id.slice(-6)}`)));
    if (!state.source) {
      sourceNote.textContent = items.length
        ? `${items.length} of ${templates.total} ад-сетов в выбранных кабинетах — выбери для копирования`
        : 'В выбранных кабинетах нет ад-сетов для копирования.';
    }
  };
  const loadSources = async () => {
    if (!state.selected.size) {
      templates = { items: [], total: 0 };
      state.source = null;
      sourceSelect.replaceChildren(el('option', { value: '' }, 'Сначала выбери аккаунт в шаге 1'));
      sourceNote.textContent = 'Выбери один или несколько кабинетов выше, чтобы увидеть их ад-сеты.';
      return;
    }
    const term = sourceSearch.value.trim();
    sourceNote.textContent = 'Загружаю…';
    try {
      templates = await api.launchTemplates({
        limit: 50,
        ad_account_ids: [...state.selected].join(','),
        ...(term ? { search: term } : {}),
      });
      state.source = null;
      renderSources();
    } catch (error) { sourceNote.textContent = error.message; }
  };
  let sourceTimer = null;
  sourceSearch.addEventListener('input', () => {
    clearTimeout(sourceTimer);
    sourceTimer = setTimeout(loadSources, 300);
  });

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
  // Creative is one of two mutually exclusive modes.
  state.creativeMode = 'new';
  state.objectStoryId = '';
  const modeNew = el('input', { type: 'radio', name: 'creative-mode', value: 'new', checked: true, style: 'width:auto' });
  const modePost = el('input', { type: 'radio', name: 'creative-mode', value: 'post', style: 'width:auto' });
  state.adSetMode = 'new';
  const adSetModeNew = el('input', { type: 'radio', name: 'adset-mode', value: 'new', checked: true, style: 'width:auto' });
  const adSetModeExisting = el('input', { type: 'radio', name: 'adset-mode', value: 'existing', style: 'width:auto' });
  const sourceLabel = el('span', {}, 'Существующий ад-сет');
  const sourceBlock = el('div', { class: 'stack', style: 'margin:.2rem 0 1rem;display:none' },
    el('span', { class: 'label' }, sourceLabel),
    el('p', { style: 'font-size:.82rem;margin:.2rem 0 .5rem' },
      'Из выбранного ад-сета берутся таргетинг, пиксель, биллинг и бюджет — всё как есть. Имена повторяются между кабинетами, поэтому в строке видны кабинет и последние цифры ID.'),
    sourceSearch, sourceSelect, sourceNote);
  // Manual targeting for a from-scratch ad set.
  const tgCountries = el('input', { type: 'text', placeholder: 'DE, AT, CH' });
  const tgAgeMin = el('input', { type: 'number', min: '13', max: '65', value: '18' });
  const tgAgeMax = el('input', { type: 'number', min: '13', max: '65', value: '65' });
  const tgGender = el('select', {}, ...['all', 'male', 'female'].map((g) => el('option', { value: g }, g)));
  const tgDestination = el('select', {}, ...['WEBSITE', 'APP'].map((d) => el('option', { value: d }, d)));
  const tgPixel = el('input', { type: 'text', placeholder: 'pixel id (optional)' });
  const tgEvent = el('select', {}, ...['', 'PURCHASE', 'LEAD', 'COMPLETE_REGISTRATION', 'ADD_TO_CART', 'INITIATE_CHECKOUT', 'VIEW_CONTENT'].map((e) => el('option', { value: e }, e || 'no pixel event')));
  const platformNames = ['facebook', 'instagram', 'audience_network', 'messenger'];
  const tgPlatforms = platformNames.map((name) => ({
    name,
    box: el('input', { type: 'checkbox', checked: name === 'facebook' || name === 'instagram', style: 'width:auto' }),
  }));
  const postPageSelect = el('select', {});
  const postSelect = el('select', { style: 'width:100%' });
  const postNote = el('div', { class: 'muted', style: 'font-size:.8rem;margin-top:.4rem' });
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

  const field2 = field; // alias to keep the blocks readable
  const newCreativeBlock = el('div', {},
    el('div', { class: 'grid-2', style: 'margin:.6rem 0' },
      field2('Page ID', pageId, 'The Page that publishes the ad'),
      field2('Instagram account ID', igActor, 'optional'),
      field2('Destination URL', link,
        'Трекинг-ссылка Keitaro с макросами. sub_id_7/sub_id_3 добавляются автоматически; главное — вести на трекинг-домен.'),
      field2('Call to action', cta),
      field2('Headline', headline),
      field2('Description', description),
      field2('Image or video', el('div', {}, mediaInput, mediaNote),
        'Загружается один раз, публикуется в каждый выбранный кабинет'),
      field2('Existing video ID', videoId, 'если видео уже в кабинете'),
    ),
    field2('Primary text', message),
    field2('URL tags', urlTags, 'дополнительные метки, напр. sub_id_5=<байер>'),
  );

  const existingPostBlock = el('div', { style: 'display:none' },
    el('p', { class: 'muted', style: 'font-size:.84rem;margin:.4rem 0 .8rem' },
      'Берём готовый пост одной из твоих Страниц как есть — новый креатив не создаётся, права на публикацию не нужны. Ссылка зашита внутри поста: для правил по Keitaro пост должен изначально вести на трекинг-домен.'),
    el('div', { class: 'grid-2' },
      field2('Страница', postPageSelect, 'Страницы выбранного подключения'),
      field2('Пост', postSelect, 'Выбери публикацию для рекламы')),
    postNote,
  );

  // --- creative mode switching ---------------------------------------------
  let loadedPagesKey = null;
  // Connections behind the selected accounts. Pages live at the connection
  // level, so a page is offered only if it exists in every selected account's
  // connection - i.e. usable across the whole batch.
  const selectedConnectionIds = () => {
    const ids = new Set();
    accounts.items.forEach((a) => { if (state.selected.has(a.id)) ids.add(a.connection_id); });
    return [...ids];
  };

  const loadPages = async () => {
    const connectionIds = selectedConnectionIds();
    if (!connectionIds.length) {
      postPageSelect.replaceChildren(el('option', { value: '' }, 'Сначала выбери аккаунт в шаге 1'));
      postNote.textContent = 'Выбери кабинеты выше, чтобы увидеть их страницы.';
      return;
    }
    const key = connectionIds.slice().sort().join(',');
    if (loadedPagesKey === key) return;
    postNote.textContent = 'Загружаю страницы…';
    try {
      const lists = await Promise.all(connectionIds.map((cid) =>
        api.assets({ connection_id: cid, types: 'page', limit: 200 }).then((r) => r.items || [])));
      // Intersect by meta_asset_id so only pages available to all selected
      // connections remain; carry the first connection's asset id to load posts.
      const counts = new Map();
      const byMeta = new Map();
      lists.forEach((pages) => {
        const seen = new Set();
        pages.forEach((pg) => {
          if (seen.has(pg.meta_asset_id)) return;
          seen.add(pg.meta_asset_id);
          counts.set(pg.meta_asset_id, (counts.get(pg.meta_asset_id) || 0) + 1);
          if (!byMeta.has(pg.meta_asset_id)) byMeta.set(pg.meta_asset_id, pg);
        });
      });
      const shared = [...byMeta.values()].filter((pg) => counts.get(pg.meta_asset_id) === connectionIds.length);
      postPageSelect.replaceChildren(
        el('option', { value: '' }, shared.length ? 'Выбери страницу' : 'Нет страниц, доступных всем выбранным кабинетам'),
        ...shared.map((pg) => el('option', { value: pg.id }, pg.name || pg.meta_asset_id)));
      loadedPagesKey = key;
      postNote.textContent = shared.length ? `${shared.length} страниц(ы)` : '';
    } catch (error) { postNote.textContent = error.message; }
  };

  postPageSelect.addEventListener('change', async () => {
    state.objectStoryId = '';
    postSelect.replaceChildren(el('option', { value: '' }, 'Загружаю посты…'));
    const assetId = postPageSelect.value;
    if (!assetId) { postSelect.replaceChildren(el('option', { value: '' }, 'Сначала выбери страницу')); return; }
    try {
      const res = await api.pagePosts(assetId, { limit: 50 });
      const posts = (res.items || []).filter((post) => post.id);
      postSelect.replaceChildren(
        el('option', { value: '' }, posts.length ? 'Выбери пост' : 'У страницы нет постов'),
        ...posts.map((post) => {
          const text = (post.message || post.story || '(без текста)').replace(/\s+/g, ' ').slice(0, 70);
          const when = post.created_time ? ' · ' + post.created_time.slice(0, 10) : '';
          return el('option', { value: post.id }, text + when);
        }));
      postNote.textContent = `${posts.length} пост(ов)`;
    } catch (error) { postSelect.replaceChildren(el('option', { value: '' }, 'Ошибка')); postNote.textContent = error.message; }
  });

  postSelect.addEventListener('change', () => { state.objectStoryId = postSelect.value; });

  const applyCreativeMode = () => {
    const post = state.creativeMode === 'post';
    existingPostBlock.style.display = post ? '' : 'none';
    newCreativeBlock.style.display = post ? 'none' : '';
    if (post) loadPages();
  };
  modeNew.addEventListener('change', () => { state.creativeMode = 'new'; applyCreativeMode(); });
  modePost.addEventListener('change', () => { state.creativeMode = 'post'; applyCreativeMode(); });

  // --- ad-set mode: new (edit fields) vs existing (copy source as-is) ------
  const targetingBlock = el('div', {},
    el('span', { class: 'label' }, 'Таргетинг (если не копируешь ад-сет)'),
    el('p', { style: 'font-size:.8rem;margin:.2rem 0 .6rem' },
      'Заполни, чтобы собрать ад-сет с нуля без источника. Если источник выбран выше — его таргетинг имеет приоритет только когда эти поля пустые.'),
    el('div', { class: 'grid-2', style: 'margin:.4rem 0' },
      field('Страны (ISO, через запятую)', tgCountries, 'напр. DE, AT, CH'),
      field('Возраст от', tgAgeMin),
      field('Возраст до', tgAgeMax),
      field('Пол', tgGender),
      field('Destination', tgDestination),
      field('Pixel ID', tgPixel, 'для оптимизации по конверсиям'),
      field('Событие пикселя', tgEvent),
      field('Плейсменты', el('div', { style: 'display:flex;gap:.9rem;flex-wrap:wrap;padding-top:.3rem' },
        ...tgPlatforms.map((pl) => el('label', { style: 'display:flex;gap:.35rem;align-items:center' },
          pl.box, el('span', { style: 'text-transform:none;letter-spacing:0;font-size:.82rem' }, pl.name)))))));

  const adSetFieldsBlock = el('div', {},
    el('div', { class: 'grid-2', style: 'margin:.6rem 0 1rem' },
      field('Ad set name', adSetName, 'Имя создаваемого ад-сета'),
      field('Daily budget', dailyBudget, 'Дневной или лайфтайм, не оба'),
      field('Lifetime budget', lifetimeBudget),
      field('Optimization goal', optimizationGoal),
      field('Billing event', billingEvent),
      field('Start', startTime),
      field('End', endTime, 'Обязателен при lifetime-бюджете')),
    targetingBlock);

  const adSetExistingNote = el('p', {
    class: 'muted', style: 'font-size:.84rem;margin:.4rem 0 1rem;display:none',
  }, 'Все настройки (бюджет, оптимизация, биллинг, таргетинг, расписание) берутся из выбранного ад-сета как есть. Выбери его в списке ниже.');

  const applyAdSetMode = () => {
    const existing = state.adSetMode === 'existing';
    sourceBlock.style.display = existing ? '' : 'none';
    adSetFieldsBlock.style.display = existing ? 'none' : '';
    adSetExistingNote.style.display = existing ? '' : 'none';
    // A from-scratch ad set has no source to inherit from.
    if (!existing) state.source = null;
  };
  adSetModeNew.addEventListener('change', () => { state.adSetMode = 'new'; applyAdSetMode(); });
  adSetModeExisting.addEventListener('change', () => { state.adSetMode = 'existing'; applyAdSetMode(); });

  // Selecting/deselecting accounts reloads the existing options they gate.
  onSelectionChange = () => {
    loadSources();
    loadedPagesKey = null;
    if (state.creativeMode === 'post') loadPages();
  };


  sourceSelect.addEventListener('change', async () => {
    const picked = (templates.items || []).find((t) => t.id === sourceSelect.value);
    if (!picked) return;
    sourceNote.textContent = 'Loading targeting and creative…';
    let template;
    try {
      template = await api.launchTemplate(picked.id);
    } catch (error) {
      sourceNote.textContent = error.message;
      return;
    }
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

    el('span', { class: 'label' }, 'New campaign'),
    el('div', { class: 'grid-2', style: 'margin:.6rem 0 1.2rem' },
      field('Campaign name', campaignName, 'The name of the campaign being created'),
      field('Objective', objective),
      field('Special ad category', specialCategory, 'Required by Meta for credit, employment, housing, politics and gambling'),
      field('Bid strategy', bidStrategy),
      field('Campaign spend cap', spendCap, 'Hard ceiling enforced by Meta itself'),
    ),

    el('span', { class: 'label' }, 'Ad set'),
    el('div', { class: 'mode-toggle' },
      el('label', { class: 'mode-option' }, adSetModeNew, el('span', {}, 'Новый ад-сет')),
      el('label', { class: 'mode-option' }, adSetModeExisting, el('span', {}, 'Существующий ад-сет'))),
    sourceBlock,
    adSetExistingNote,
    adSetFieldsBlock,

    el('span', { class: 'label' }, 'Creative'),
    el('div', { class: 'mode-toggle' },
      el('label', { class: 'mode-option' }, modeNew, el('span', {}, 'Новый креатив')),
      el('label', { class: 'mode-option' }, modePost, el('span', {}, 'Существующий пост'))),

    newCreativeBlock,
    existingPostBlock,
  ));

  // 3 · checkpoints ----------------------------------------------------------
  const ladder = checkpointEditor([
    { spend: 5, min_tracker_clicks: 20 },
    { spend: 15, min_tracker_leads: 1 },
    { spend: 40, min_tracker_sales: 1 },
  ]);

  container.append(el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, '3 · Checkpoint ladder')),
    el('p', { style: 'margin-bottom:.8rem;font-size:.84rem' },
      'Each rung is judged when lifetime spend crosses it: every minimum listed must already be met or the campaign is paused. FB clicks and impressions come from Insights; tracker clicks, registrations and deposits come from Keitaro. Guards only pause - a passed rung is never re-checked, and a manually resumed campaign is only judged again at the next rung.'),
    ladder.node,
  ));

  // 4 · launch ---------------------------------------------------------------
  const status = el('div', {});
  const previewBox = el('div', {});

  const buildPayload = () => {
    const selected = [...state.selected];
    const localTime = (value) => (value ? `${value}:00+0000`.replace('T', 'T') : '');
    const payload = {
      connection_id: (accounts.items.find((a) => a.id === selected[0]) || {}).connection_id,
      name: campaignName.value.trim(),
      idempotency_key: `launch-${Date.now()}`,
      ad_account_ids: selected,
      checkpoints: ladder.read(),
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
          // Manual targeting (used when no source ad set is copied).
          countries: tgCountries.value.split(',').map((x) => x.trim().toUpperCase()).filter(Boolean),
          age_min: Number(tgAgeMin.value) || 0,
          age_max: Number(tgAgeMax.value) || 0,
          gender: tgGender.value,
          platforms: tgPlatforms.filter((pl) => pl.box.checked).map((pl) => pl.name),
          pixel_id: tgPixel.value.trim(),
          custom_event_type: tgEvent.value || undefined,
          destination_type: tgDestination.value || undefined,
        },
        creative: {
          object_story_id: state.creativeMode === 'post' ? (state.objectStoryId || '') : '',
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
    if (state.creativeMode === 'post' && !state.objectStoryId) {
      status.replaceChildren(el('div', { class: 'error' },
        el('strong', {}, 'Пост не выбран'),
        el('span', {}, 'Выбери страницу и пост, либо переключись на «Новый креатив».')));
      return false;
    }
    const hasManualGeo = tgCountries.value.split(',').map((x) => x.trim()).filter(Boolean).length > 0;
    if (state.adSetMode === 'existing' && !state.source) {
      status.replaceChildren(el('div', { class: 'error' },
        el('strong', {}, 'Исходный ад-сет не выбран'),
        el('span', {}, 'В режиме «Существующий ад-сет» выбери ад-сет для копирования, либо переключись на «Новый ад-сет».')));
      return false;
    }
    if (state.adSetMode === 'new' && !state.source && !hasManualGeo) {
      status.replaceChildren(el('div', { class: 'error' },
        el('strong', {}, 'Нет таргетинга'),
        el('span', {}, 'Для нового ад-сета укажи страны в блоке «Таргетинг», либо выбери исходный ад-сет для копирования настроек.')));
      return false;
    }
    return true;
  };

  // The launcher always injects the campaign-id macro, so tracking data will
  // flow *if* the click routes through Keitaro. Whether the destination link
  // is a Keitaro tracking URL is the operator's call - so when a checkpoint
  // depends on tracker metrics, make that dependency explicit before spend
  // starts, with a modal the operator has to acknowledge.
  const trackerWarning = () => {
    const usesTracker = ladder.read().some((c) =>
      c.min_tracker_clicks || c.min_tracker_leads || c.min_tracker_sales || c.min_tracker_revenue);
    if (!usesTracker) return Promise.resolve(true);
    const linkVal = (link.value || '') + '&' + (urlTags.value || '');
    const usingPost = state.creativeMode === 'post';
    // An existing post carries its own link; we cannot see whether it points
    // at the tracker, so it is always the "verify" case, never auto-green.
    const looksTracked = !usingPost && /sub_id_7=/i.test(linkVal);
    return new Promise((resolve) => {
      const dialog = el('dialog', { style: 'max-width:32rem' },
        el('h3', { style: 'margin:0 0 .6rem' }, 'Правила зависят от Keitaro'),
        el('p', { style: 'font-size:.9rem;line-height:1.5' },
          'В чекпоинтах есть пороги по трекеру (реги / депозиты / трекер-клики). Они сработают только если ',
          el('strong', {}, 'ссылка объявления ведёт на трекинг-домен Keitaro'),
          '. sub_id_7={{campaign.id}} добавляется автоматически, но если ссылка ведёт мимо трекера, реги/депы придут нулями и гвард ',
          el('strong', {}, 'остановит кампанию'),
          ', даже если она реально работает.'),
        looksTracked
          ? el('p', { class: 'pill ok', style: 'display:inline-block' }, 'Ссылка размечена sub_id_7 — похоже на трекинг-ссылку')
          : el('p', { class: 'pill bad', style: 'display:inline-block' }, 'В ссылке нет sub_id_7 — убедись, что это трекинг-ссылка Keitaro'),
        el('div', { style: 'display:flex;gap:.6rem;margin-top:1rem;justify-content:flex-end' },
          el('button', { class: 'button', onclick: () => { dialog.close(); resolve(false); } }, 'Отмена'),
          el('button', { class: 'button primary', onclick: () => { dialog.close(); resolve(true); } }, 'Ссылка на Keitaro — запустить')));
      document.body.append(dialog);
      dialog.addEventListener('close', () => dialog.remove());
      dialog.showModal();
    });
  };

  container.append(el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, '4 · Review and launch')),
    el('p', { style: 'margin-bottom:.9rem;font-size:.84rem' },
      'Preview собирает запрос локально и показывает финальную иерархию — бесплатно, ничего в Meta не создаётся. Launch публикует по-настоящему.'),
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
              ...(result.checkpoints || []).map((checkpoint) => el('div', { class: 'pill info', style: 'margin:.3rem .3rem 0 0' },
                `at $${checkpoint.spend}`)),
              el('pre', { class: 'token-reveal', style: 'margin-top:.7rem;white-space:pre-wrap;max-height:22rem;overflow:auto' },
                JSON.stringify(result.hierarchy, null, 2)),
            ));
          } catch (error) { showError(error); } finally { button.disabled = false; }
        },
      }, 'Preview'),
      el('button', {
        class: 'button primary',
        onclick: async (event) => {
          if (!guardSelection()) return;
          if (!(await trackerWarning())) return;
          const rungs = ladder.read().length;
          if (!window.confirm(`Publish into ${state.selected.size} ad account(s) with ${rungs} checkpoint(s)?`)) return;
          const button = event.currentTarget;
          const label = button.textContent;
          button.disabled = true;
          button.textContent = 'Публикуем…';
          status.replaceChildren(el('div', { class: 'card panel', style: 'display:flex;gap:.6rem;align-items:center' },
            el('span', { class: 'spinner' }), el('span', {}, 'Отправляем в Meta и ждём ответ…')));
          try {
            // Stay on the launcher; report the real outcome here rather than
            // redirecting before Meta has answered.
            const result = await api.launch(buildPayload());
            status.replaceChildren();
            if (result.warning) {
              status.replaceChildren(el('div', { class: 'error' },
                el('strong', {}, 'Опубликовано, но гвард не полностью прикреплён'), el('span', {}, result.warning)));
              toast('Опубликовано с оговоркой — проверь гвард', 'ok');
            } else {
              status.replaceChildren(el('div', { class: 'card panel', style: 'border-color:var(--ok)' },
                el('strong', {}, `Готово — запущено в ${state.selected.size} кабинет(ов).`),
                el('a', { class: 'button small', href: '#/campaigns', style: 'margin-left:.6rem' }, 'Открыть кампании')));
              toast(`Запущено в ${state.selected.size} кабинет(ов)`, 'ok');
            }
          } catch (error) {
            showError(error);
          } finally { button.disabled = false; button.textContent = label; }
        },
      }, 'Launch'),
    ),
  ));

  applyCreativeMode();
  applyAdSetMode();
  loadSources();
  return container;
}
