// History: what the workers have been doing, and what broke.

import { api } from '../api.js';
import { el, int, relative, pill, dateTime } from '../format.js';
import { head, empty, metric, table, toast } from '../shell.js';

const filters = { status: '', type: '' };

const JOB_LABELS = {
  sync_connection: 'Discover inventory',
  publish_account: 'Publish to ad account',
  collect_insights: 'Insights (published objects)',
  evaluate_rules: 'Evaluate rules',
  sync_ad_entities: 'Inventory sweep',
  collect_account_insights: 'Insights (account-wide)',
  backfill_insights: 'Backfill history',
  repair_insight_gaps: 'Repair gaps',
  collect_windowed_reach: 'Deduplicated reach',
  retention_sweep: 'Retention sweep',
};

export async function historyView() {
  const [page, batches, rules] = await Promise.all([
    api.jobs({ ...filters, limit: 200 }),
    api.batches({ limit: 20 }),
    api.rules({ limit: 50 }),
  ]);
  const jobs = page.items || [];

  const container = el('div', {});
  container.append(head('History', 'Launches and background work'));

  // The dedicated batch and rule pages are gone, but a failed launch still
  // has to be diagnosable: the per-account Graph code is the only thing that
  // says why Meta refused, and a guard that never fires is worth seeing.
  container.append(await launchesPanel(batches));
  container.append(guardsPanel(rules));

  const counts = jobs.reduce((acc, job) => {
    acc[job.status] = (acc[job.status] || 0) + 1;
    return acc;
  }, {});

  container.append(el('div', { class: 'metrics' },
    metric('Succeeded', int(counts.succeeded || 0)),
    metric('Pending', int(counts.pending || 0)),
    metric('Running', int(counts.running || 0)),
    metric('Dead', int(counts.dead || 0),
      counts.dead ? 'Exhausted every retry' : 'Nothing stuck', !counts.dead),
  ));

  const statusSelect = el('select', {},
    ...['', 'pending', 'running', 'succeeded', 'failed', 'dead'].map((status) =>
      el('option', { value: status, selected: status === filters.status }, status || 'Any status')));
  const typeSelect = el('select', {},
    el('option', { value: '' }, 'Any job'),
    ...Object.entries(JOB_LABELS).map(([value, label]) =>
      el('option', { value, selected: value === filters.type }, label)));

  container.append(el('form', { class: 'card filters', onsubmit: (e) => e.preventDefault() },
    el('label', { class: 'field' }, el('span', {}, 'Status'), statusSelect),
    el('label', { class: 'field' }, el('span', {}, 'Job'), typeSelect),
    el('button', {
      class: 'button primary',
      onclick: () => {
        filters.status = statusSelect.value;
        filters.type = typeSelect.value;
        window.dispatchEvent(new CustomEvent('route:refresh'));
      },
    }, 'Apply'),
  ));

  const rows = jobs.map((job) => el('tr', {},
    el('td', { class: 'name' },
      el('span', {}, JOB_LABELS[job.type] || job.type),
      el('span', { class: 'sub' }, job.type),
    ),
    el('td', {}, pill(job.status)),
    el('td', { class: 'num' }, `${job.attempts || 0}/${job.max_attempts || 0}`),
    el('td', { class: 'numeric' }, relative(job.created_at)),
    el('td', { class: 'numeric' }, job.available_at ? dateTime(job.available_at) : '—'),
    el('td', { class: 'muted', style: 'font-size:.78rem;max-width:26rem' }, job.last_error || ''),
  ));

  container.append(table([
    { label: 'Job' }, { label: 'Status' }, { label: 'Attempts', align: 'right' },
    { label: 'Created' }, { label: 'Runs at' }, { label: 'Last error' },
  ], rows) || empty('No jobs match', 'Adjust the filters above.'));

  if (counts.dead) {
    // Dead jobs hold their dedupe key, so the same work will not be queued
    // again until the next interval bucket. Worth saying out loud.
    container.append(el('div', { class: 'card panel', style: 'margin-top:1rem' },
      el('span', { class: 'label' }, 'About dead jobs'),
      el('p', { style: 'margin-top:.5rem;font-size:.86rem' },
        'A dead job has exhausted every retry. It keeps its deduplication key, so identical work will not be scheduled again until the next interval window.'),
    ));
  }
  return container;
}

/** Recent launches with their per-account outcome. */
async function launchesPanel(batches) {
  const items = batches.items || [];
  if (!items.length) {
    return el('section', { class: 'card panel' },
      el('header', {}, el('span', { class: 'label' }, 'Recent launches')),
      el('p', { style: 'font-size:.86rem' }, 'Nothing published yet.'));
  }

  const body = el('div', {});
  const rows = items.map((batch) => el('tr', {},
    el('td', { class: 'name' },
      el('span', {}, batch.name || 'untitled'),
      el('span', { class: 'sub' }, batch.idempotency_key || batch.id)),
    el('td', {}, pill(batch.status)),
    el('td', { class: 'num' },
      `${batch.succeeded_accounts || 0} ok · ${batch.failed_accounts || 0} failed · ${batch.total_accounts || 0}`),
    el('td', { class: 'numeric' }, relative(batch.created_at)),
    el('td', { class: 'num' }, el('div', { style: 'display:flex;gap:.4rem;justify-content:flex-end' },
      // Anything still running has to be stoppable from here: the launcher
      // can start spend, so the same product has to be able to end it.
      ['queued', 'running', 'succeeded', 'partially_succeeded'].includes(batch.status)
        ? el('button', {
            class: 'button small danger',
            onclick: async (event) => {
              if (!window.confirm(`Pause everything "${batch.name || batch.id}" published?`)) return;
              const button = event.currentTarget;
              button.disabled = true;
              try {
                const stopped = await api.stopBatch(batch.id);
                const r = stopped.result || {};
                toast(`Paused ${r.paused || 0}, already paused ${r.skipped || 0}`
                  + (r.failed ? `, failed ${r.failed}` : ''), r.failed ? 'bad' : 'ok');
                window.dispatchEvent(new CustomEvent('route:refresh'));
              } catch (error) {
                toast(error.message, 'bad');
                button.disabled = false;
              }
            },
          }, 'Stop')
        : null,
      (batch.failed_accounts || 0) > 0
      ? el('button', {
          class: 'button small',
          onclick: async (event) => {
            event.currentTarget.disabled = true;
            const results = await api.batchResults(batch.id, { limit: 100 });
            body.replaceChildren(el('div', { class: 'card panel' },
              el('span', { class: 'label' }, `Per ad account · ${batch.name || batch.id}`),
              table([
                { label: 'Ad account' }, { label: 'Status' },
                { label: 'Graph code' }, { label: 'Error' },
              ], (results.items || []).map((result) => el('tr', {},
                el('td', { class: 'mono', style: 'font-size:.78rem' }, result.ad_account_id),
                el('td', {}, pill(result.status)),
                // The code and subcode are what make a Meta rejection
                // actionable; the prose alone rarely is.
                el('td', { class: 'mono', style: 'font-size:.78rem' },
                  result.error_code ? `${result.error_code}/${result.error_subcode || 0}` : ''),
                el('td', { class: 'muted', style: 'font-size:.8rem' }, result.error_message || ''),
              ))) || el('p', {}, 'No per-account detail recorded.'),
            ));
          },
        }, 'Why')
      : null)),
  ));

  return el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, 'Recent launches')),
    table([
      { label: 'Launch' }, { label: 'Status' }, { label: 'Accounts', align: 'right' },
      { label: 'When' }, { label: '', align: 'right' },
    ], rows),
    body,
  );
}

/** Active guards and whether they have ever fired. */
function guardsPanel(rules) {
  const items = rules.items || [];
  if (!items.length) return el('div', {});

  return el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, 'Guards')),
    table([
      { label: 'Guard' }, { label: 'Status' }, { label: 'Last check' },
      { label: 'Last pause' }, { label: '', align: 'right' },
    ], items.map((rule) => el('tr', {},
      el('td', { class: 'name' },
        el('span', {}, rule.name),
        el('span', { class: 'sub' }, `${rule.scope_level} · every ${Math.round((rule.evaluation_interval_seconds || 0))}s`)),
      el('td', {}, pill(rule.status)),
      el('td', { class: 'numeric' }, relative(rule.last_evaluated_at)),
      el('td', { class: 'numeric' }, rule.last_triggered_at ? relative(rule.last_triggered_at) : '—'),
      el('td', { class: 'num' }, el('button', {
        class: 'button small',
        onclick: async (event) => {
          const button = event.currentTarget;
          button.disabled = true;
          try {
            if (rule.status === 'active') await api.disableRule(rule.id);
            else await api.enableRule(rule.id);
            window.dispatchEvent(new CustomEvent('route:refresh'));
          } catch { button.disabled = false; }
        },
      }, rule.status === 'active' ? 'Disable' : 'Enable')),
    ))),
  );
}
