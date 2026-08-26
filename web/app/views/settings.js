// Settings: connections and programmatic access.

import { api } from '../api.js';
import { el, dateTime, relative, pill } from '../format.js';
import { head, empty, table, toast, state } from '../shell.js';

export async function settingsView() {
  const [connections, keys] = await Promise.all([
    api.connections({ limit: 50 }),
    api.apiKeys(),
  ]);

  const container = el('div', {});
  container.append(head('Settings', 'Account'));

  container.append(el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, 'Signed in as')),
    el('dl', { class: 'kv' },
      el('dt', {}, 'Username'), el('dd', {}, state.user.username),
      el('dt', {}, 'Email'), el('dd', {}, state.user.email),
      el('dt', {}, 'Role'), el('dd', {}, pill(state.user.role)),
    ),
  ));

  const connectionRows = (connections.items || []).map((connection) => el('tr', {},
    el('td', { class: 'name' },
      el('span', {}, connection.display_name || connection.meta_user_id),
      el('span', { class: 'sub' }, connection.meta_user_id),
    ),
    el('td', {}, pill(connection.status)),
    el('td', { class: 'numeric' }, relative(connection.last_synced_at)),
    el('td', { class: 'num' },
      el('button', {
        class: 'button small danger',
        onclick: async (event) => {
          if (!window.confirm('Revoke this connection? Meta permissions are withdrawn and its data stops updating.')) return;
          event.currentTarget.disabled = true;
          try {
            await api.revokeConnection(connection.id);
            toast('Connection revoked', 'ok');
            window.dispatchEvent(new CustomEvent('route:refresh'));
          } catch (error) {
            toast(error.message, 'bad');
            event.currentTarget.disabled = false;
          }
        },
      }, 'Revoke')),
  ));

  container.append(el('section', { class: 'card panel' },
    el('header', {},
      el('span', { class: 'label' }, 'Meta connections'),
      el('a', { class: 'button small', href: '/app/connect/meta' }, 'Connect'),
    ),
    table([{ label: 'Account' }, { label: 'Status' }, { label: 'Last sync' }, { label: '', align: 'right' }],
      connectionRows) || el('p', {}, 'No Meta account connected.'),
  ));

  const keyList = el('div', {});
  const renderKeys = (items) => {
    keyList.replaceChildren(
      table([{ label: 'Name' }, { label: 'Created' }, { label: 'Last used' }, { label: '', align: 'right' }],
        (items || []).map((key) => el('tr', {},
          el('td', {}, key.name || 'unnamed'),
          el('td', { class: 'numeric' }, dateTime(key.created_at)),
          el('td', { class: 'numeric' }, key.last_used_at ? relative(key.last_used_at) : 'never'),
          el('td', { class: 'num' },
            el('button', {
              class: 'button small danger',
              onclick: async (event) => {
                event.currentTarget.disabled = true;
                try {
                  await api.revokeApiKey(key.id);
                  renderKeys((await api.apiKeys()).items);
                  toast('Key revoked', 'ok');
                } catch (error) {
                  toast(error.message, 'bad');
                  event.currentTarget.disabled = false;
                }
              },
            }, 'Revoke')),
        ))) || el('p', {}, 'No API keys yet.'),
    );
  };
  renderKeys(keys.items);

  const nameInput = el('input', { type: 'text', placeholder: 'ci-pipeline' });
  const reveal = el('div', {});

  container.append(el('section', { class: 'card panel' },
    el('header', {}, el('span', { class: 'label' }, 'API keys')),
    el('p', { style: 'margin-bottom:.9rem;font-size:.86rem' },
      'A key authenticates scripts as you, with your tenant scope. Only its hash is stored, so the token below is shown once and cannot be recovered.'),
    el('div', { style: 'display:flex;gap:.6rem;align-items:end;margin-bottom:1rem;flex-wrap:wrap' },
      el('label', { class: 'field', style: 'flex:1;min-width:12rem' }, el('span', {}, 'Name'), nameInput),
      el('button', {
        class: 'button primary',
        onclick: async (event) => {
          const button = event.currentTarget;
          button.disabled = true;
          try {
            const created = await api.createApiKey(nameInput.value.trim());
            nameInput.value = '';
            reveal.replaceChildren(
              el('div', { class: 'card panel' },
                el('span', { class: 'label' }, 'Copy this token now'),
                el('div', { class: 'token-reveal', style: 'margin-top:.6rem' }, created.token),
                el('p', { style: 'margin-top:.5rem;font-size:.8rem' },
                  'It will not be shown again. Send it as: Authorization: Bearer <token>'),
              ),
            );
            renderKeys((await api.apiKeys()).items);
            toast('API key created', 'ok');
          } catch (error) {
            toast(error.message, 'bad');
          } finally {
            button.disabled = false;
          }
        },
      }, 'Create key'),
    ),
    reveal,
    keyList,
  ));

  return container;
}
