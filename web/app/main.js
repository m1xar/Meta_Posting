// Entry point: decide signed-in or not, then hand over to the shell.

import { api, ApiError } from './api.js';
import { renderAuth } from './auth.js';
import { mount, registerView, state } from './shell.js';

import { overviewView } from './views/overview.js';
import { launcherView } from './views/launcher.js';
import { campaignsView } from './views/campaigns.js';
import { accountView } from './views/account.js';
import { settingsView } from './views/settings.js';
import { adminView } from './views/admin.js';

registerView('overview', overviewView);
registerView('launcher', launcherView);
registerView('campaigns', campaignsView);
registerView('account', (params) => accountView(params));
registerView('settings', settingsView);
registerView('admin', adminView);

const root = document.getElementById('root');

function start(user) {
  state.user = user;
  if (!window.location.hash) window.location.hash = '#/overview';
  mount(root);
}

window.addEventListener('hashchange', () => { if (state.user) mount(root); });
window.addEventListener('route:refresh', () => { if (state.user) mount(root); });

(async () => {
  try {
    // The session cookie is HttpOnly, so identity has to come from the
    // server. A 401 here is the signal to show the sign-in gate.
    const identity = await api.me();
    if (!identity.user) throw new ApiError(403, 'no_tenant', 'This credential owns no workspace.');
    start(identity.user);
  } catch (error) {
    if (error instanceof ApiError && (error.isAuth || error.isForbidden)) {
      renderAuth(root, start);
      return;
    }
    root.innerHTML = '';
    root.append(Object.assign(document.createElement('div'), {
      className: 'auth-page',
      textContent: `Cannot reach the API: ${error.message}`,
    }));
  }
})();
