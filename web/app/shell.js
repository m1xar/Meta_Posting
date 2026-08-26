// Application shell: rail, routing, session state.

import { api, ApiError } from './api.js';
import { el, clear } from './format.js';

const SECTIONS = [
  {
    group: 'Operate',
    items: [
      { id: 'overview', label: 'Dashboard' },
      { id: 'launcher', label: 'Launcher' },
      { id: 'campaigns', label: 'Campaigns' },
    ],
  },
  {
    group: 'Manage',
    items: [
      { id: 'settings', label: 'Settings' },
    ],
  },
  {
    group: 'Admin',
    adminOnly: true,
    items: [{ id: 'admin', label: 'All tenants' }],
  },
];

export const state = {
  user: null,
  views: new Map(),
};

export const isAdmin = () => state.user && state.user.role === 'admin';

/** Toasts are for outcomes of actions the user took; errors inside a view
 *  render in the view itself, where the context is. */
export function toast(message, kind = '') {
  let host = document.querySelector('.toast-host');
  if (!host) {
    host = el('div', { class: 'toast-host' });
    document.body.append(host);
  }
  const node = el('div', { class: `toast ${kind}` }, message);
  host.append(node);
  setTimeout(() => node.remove(), 5200);
}

export function registerView(id, render) {
  state.views.set(id, render);
}

function currentRoute() {
  const hash = window.location.hash.replace(/^#\/?/, '');
  const [id, ...rest] = hash.split('/');
  return { id: id || 'overview', params: rest };
}

function renderRail(active) {
  const nav = el('nav', { class: 'rail-nav' });
  for (const section of SECTIONS) {
    if (section.adminOnly && !isAdmin()) continue;
    nav.append(el('div', { class: 'rail-group label' }, section.group));
    for (const item of section.items) {
      nav.append(el('a', {
        class: `rail-link${item.id === active ? ' active' : ''}`,
        href: `#/${item.id}`,
      }, el('span', {}, item.label)));
    }
  }

  return el('aside', { class: 'rail' },
    el('a', { class: 'rail-brand', href: '#/overview' },
      el('span', { class: 'brand-mark' }, 'R'),
      el('span', { class: 'brand-name', html: 'Raze<br>Ads' }),
    ),
    nav,
    el('div', { class: 'rail-foot' },
      el('div', { class: 'rail-user' },
        el('strong', {}, state.user ? state.user.username : ''),
        el('span', { class: 'label' }, state.user ? state.user.role : ''),
      ),
      el('button', {
        class: 'button small',
        onclick: async () => {
          try { await api.logout(); } catch { /* the cookie is cleared regardless */ }
          window.location.reload();
        },
      }, 'Sign out'),
    ),
  );
}

export async function mount(root) {
  const route = currentRoute();
  const render = state.views.get(route.id) || state.views.get('overview');

  clear(root);
  const main = el('main', { class: 'main' });
  root.append(el('div', { class: 'shell' }, renderRail(route.id), main));

  main.append(el('div', { class: 'stack' },
    el('div', { class: 'skeleton', style: 'width:9rem' }),
    el('div', { class: 'skeleton', style: 'width:60%;height:2rem' }),
  ));

  try {
    const view = await render(route.params);
    clear(main).append(view);
  } catch (error) {
    if (error instanceof ApiError && error.isAuth) {
      window.location.reload();
      return;
    }
    clear(main).append(viewError(error));
  }
}

export function viewError(error) {
  const message = error instanceof ApiError ? error.message : String(error && error.message || error);
  return el('div', { class: 'error' },
    el('strong', {}, 'Could not load this view'),
    el('span', {}, message),
  );
}

export function head(title, subtitle, ...actions) {
  return el('header', { class: 'view-head' },
    el('div', { class: 'titles' },
      el('p', { class: 'eyebrow' }, subtitle),
      el('h1', {}, title),
    ),
    actions.length ? el('div', { class: 'actions' }, ...actions) : null,
  );
}

export function empty(title, body, action) {
  return el('div', { class: 'card empty' },
    el('h3', {}, title),
    el('p', {}, body),
    action || null,
  );
}

export function metric(label, value, note, unknown = false) {
  return el('div', { class: `card metric${unknown ? ' unknown' : ''}` },
    el('span', { class: 'label' }, label),
    el('span', { class: 'value' }, value),
    note ? el('span', { class: 'note' }, note) : null,
  );
}

export function table(columns, rows) {
  if (!rows.length) return null;
  return el('div', { class: 'table-wrap' },
    el('table', {},
      el('thead', {}, el('tr', {}, ...columns.map((column) =>
        el('th', { style: column.align === 'right' ? 'text-align:right' : null }, column.label)))),
      el('tbody', {}, ...rows),
    ),
  );
}

/** A "show more" control for lists that outgrow one page.
 *
 * The tables here can hold thousands of rows - one profile has 6,887 ad sets
 * - so a fixed limit silently hides most of the account. This makes the
 * boundary visible and crossable instead. */
export function moreRow(shown, total, onMore) {
  if (shown >= total) {
    return el('p', { class: 'muted', style: 'margin-top:.8rem;font-size:.8rem' },
      `${shown} of ${total}.`);
  }
  return el('div', { class: 'row-between', style: 'margin-top:.9rem' },
    el('span', { class: 'muted', style: 'font-size:.8rem' }, `Showing ${shown} of ${total}.`),
    el('button', {
      class: 'button small',
      onclick: (event) => {
        event.currentTarget.disabled = true;
        event.currentTarget.textContent = 'Loading...';
        onMore();
      },
    }, 'Show more'),
  );
}
