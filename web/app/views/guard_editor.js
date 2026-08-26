// The checkpoint-ladder editor, shared by the launcher and the campaigns page.
//
// A ladder reads the way a buyer states the rule: "at $5 I want 20 tracker
// clicks, at $15 the first registration, at $40 the first deposit". Each rung
// is a spend threshold plus the minimums that must already be met when
// lifetime spend crosses it; a rung that is not met pauses the campaign.

import { el } from '../format.js';

const FIELDS = [
  ['spend', 'After spend $'],
  ['min_clicks', 'FB clicks ≥'],
  ['min_impressions', 'Impressions ≥'],
  ['min_tracker_clicks', 'Trk clicks ≥'],
  ['min_tracker_leads', 'Regs ≥'],
  ['min_tracker_sales', 'Deposits ≥'],
  ['min_tracker_revenue', 'Revenue $ ≥'],
];

function describe(checkpoint) {
  const spend = Number(checkpoint.spend) || 0;
  const needs = [];
  if (checkpoint.min_clicks) needs.push(`${checkpoint.min_clicks} FB clicks`);
  if (checkpoint.min_impressions) needs.push(`${checkpoint.min_impressions} impressions`);
  if (checkpoint.min_tracker_clicks) needs.push(`${checkpoint.min_tracker_clicks} tracker clicks`);
  if (checkpoint.min_tracker_leads) needs.push(`${checkpoint.min_tracker_leads} reg(s)`);
  if (checkpoint.min_tracker_sales) needs.push(`${checkpoint.min_tracker_sales} deposit(s)`);
  if (checkpoint.min_tracker_revenue) needs.push(`$${checkpoint.min_tracker_revenue} revenue`);
  if (!spend) return 'Set a spend threshold';
  if (!needs.length) return `At $${spend}: add at least one minimum`;
  return `At $${spend} spent, pause unless: ${needs.join(', ')}`;
}

/** Builds the editor. Returns { node, read }, where read() yields the
 *  checkpoint array exactly as the API expects it. */
export function checkpointEditor(initial) {
  const rows = [];
  const body = el('div', { class: 'stack' });

  const addRow = (values = {}) => {
    const inputs = {};
    const summary = el('span', { class: 'pill info' }, describe(values));
    const sync = () => {
      const current = readRow(inputs);
      summary.textContent = describe(current);
    };
    const grid = el('div', {
      style: 'display:grid;grid-template-columns:repeat(auto-fit,minmax(7.5rem,1fr));gap:.6rem;align-items:end',
    });
    for (const [name, label] of FIELDS) {
      const input = el('input', {
        type: 'number', min: '0', step: 'any',
        value: values[name] !== undefined && values[name] !== 0 ? String(values[name]) : '',
        placeholder: name === 'spend' ? 'required' : '—',
      });
      input.addEventListener('input', sync);
      inputs[name] = input;
      grid.append(el('label', { class: 'field' }, el('span', {}, label), input));
    }
    const card = el('div', { class: 'card', style: 'padding:.9rem;display:grid;gap:.7rem' },
      grid,
      el('div', { class: 'row-between' },
        summary,
        el('button', {
          class: 'button small danger',
          onclick: () => {
            const index = rows.findIndex((row) => row.card === card);
            if (index >= 0) rows.splice(index, 1);
            card.remove();
            renderEmpty();
          },
        }, 'Remove')),
    );
    rows.push({ card, inputs });
    body.append(card);
    renderEmpty();
  };

  const emptyNote = el('p', { style: 'font-size:.84rem' },
    'No checkpoints. The launch will run unguarded until something else stops it.');
  const renderEmpty = () => {
    if (rows.length) emptyNote.remove();
    else if (!emptyNote.isConnected) body.append(emptyNote);
  };

  const readRow = (inputs) => {
    const checkpoint = {};
    for (const [name] of FIELDS) {
      const value = Number(inputs[name].value);
      if (value > 0) checkpoint[name] = ['spend', 'min_tracker_leads', 'min_tracker_sales', 'min_tracker_revenue'].includes(name)
        ? value : Math.round(value);
    }
    return checkpoint;
  };

  const node = el('div', { class: 'stack' },
    body,
    el('button', { class: 'button small', onclick: () => addRow() }, 'Add checkpoint'),
  );

  (initial && initial.length ? initial : []).forEach(addRow);
  renderEmpty();

  return {
    node,
    read: () => rows.map((row) => readRow(row.inputs)).filter((checkpoint) => (checkpoint.spend || 0) > 0),
  };
}
