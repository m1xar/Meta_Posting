// Sign-in / sign-up gate.

import { api } from './api.js';
import { el, clear } from './format.js';

export function renderAuth(root, onSignedIn) {
  let mode = 'login';

  const error = el('div', {});
  const identifier = el('input', { type: 'text', autocomplete: 'username', placeholder: 'you@example.com or username' });
  const email = el('input', { type: 'email', autocomplete: 'email', placeholder: 'you@example.com' });
  const username = el('input', { type: 'text', autocomplete: 'username', placeholder: 'buyer.name', minlength: '3', maxlength: '64' });
  const password = el('input', { type: 'password', autocomplete: 'current-password', minlength: '10', maxlength: '128' });

  const loginFields = el('div', { class: 'stack' },
    el('label', { class: 'field' }, el('span', {}, 'Email or username'), identifier));
  const registerFields = el('div', { class: 'stack' },
    el('label', { class: 'field' }, el('span', {}, 'Email'), email),
    el('label', { class: 'field' }, el('span', {}, 'Username'), username));

  const fields = el('div', {}, loginFields);
  const submit = el('button', { class: 'button primary', type: 'submit', style: 'width:100%' }, 'Sign in');

  const tabs = el('div', { class: 'auth-tabs' },
    el('button', { type: 'button', class: 'active' }, 'Sign in'),
    el('button', { type: 'button' }, 'Create account'));

  const setMode = (next) => {
    mode = next;
    error.replaceChildren();
    tabs.children[0].classList.toggle('active', next === 'login');
    tabs.children[1].classList.toggle('active', next === 'register');
    clear(fields).append(next === 'login' ? loginFields : registerFields);
    submit.textContent = next === 'login' ? 'Sign in' : 'Create account';
    password.setAttribute('autocomplete', next === 'login' ? 'current-password' : 'new-password');
  };
  tabs.children[0].addEventListener('click', () => setMode('login'));
  tabs.children[1].addEventListener('click', () => setMode('register'));

  const form = el('form', {
    class: 'stack',
    onsubmit: async (event) => {
      event.preventDefault();
      error.replaceChildren();
      submit.disabled = true;
      try {
        const result = mode === 'login'
          ? await api.login(identifier.value.trim(), password.value)
          : await api.register({
              email: email.value.trim(),
              username: username.value.trim(),
              password: password.value,
            });
        onSignedIn(result.user);
      } catch (err) {
        error.append(el('div', { class: 'error' },
          el('strong', {}, err.code === 'conflict' ? 'Already taken' : 'Could not continue'),
          el('span', {}, err.message),
        ));
        submit.disabled = false;
      }
    },
  },
    fields,
    el('label', { class: 'field' }, el('span', {}, 'Password'), password),
    error,
    submit,
  );

  clear(root).append(el('div', { class: 'auth-page' },
    el('div', { class: 'card auth-card rise' },
      el('span', { class: 'brand-mark' }, 'R'),
      el('p', { class: 'eyebrow' }, 'Raze Ads'),
      el('h1', { style: 'font-size:1.7rem' }, 'Workspace'),
      el('p', { style: 'font-size:.9rem' },
        'Operate Meta advertising: publish in batches, track spend across every ad account, and stop what is not working.'),
      tabs,
      form,
    ),
  ));
}
