// Thin wrapper over the JSON API.
//
// Everything goes through /v1, which is tenant-scoped: the server restricts
// each response to the caller's own connections, so the client never has to
// filter for ownership and cannot leak another tenant's rows by forgetting.

/** The CSRF cookie is readable by design - it is the double-submit half. */
function csrfToken() {
  const match = document.cookie.match(/(?:^|;\s*)raze_csrf=([^;]*)/);
  return match ? decodeURIComponent(match[1]) : '';
}

export class ApiError extends Error {
  constructor(status, code, message, details) {
    super(message || `request failed with ${status}`);
    this.status = status;
    this.code = code;
    this.details = details;
  }

  get isAuth() { return this.status === 401; }
  get isForbidden() { return this.status === 403; }
}

async function request(method, path, body) {
  const headers = {};
  const init = { method, headers, credentials: 'same-origin' };

  if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(body);
  }
  // Session cookies are ambient, so mutations carry the double-submit token.
  // Bearer callers do not need it and the server does not ask them for one.
  if (method !== 'GET') headers['X-CSRF-Token'] = csrfToken();

  const response = await fetch(path, init);
  if (response.status === 204) return null;

  const text = await response.text();
  let payload = null;
  if (text) {
    try { payload = JSON.parse(text); } catch { payload = null; }
  }

  if (!response.ok) {
    const error = payload && payload.error ? payload.error : {};
    throw new ApiError(response.status, error.code, error.message, error.details);
  }
  return payload;
}

const query = (params) => {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params || {})) {
    if (value === undefined || value === null || value === '') continue;
    search.set(key, value);
  }
  const encoded = search.toString();
  return encoded ? `?${encoded}` : '';
};

export const api = {
  // auth
  register: (payload) => request('POST', '/auth/register', payload),
  login: (identifier, password) => request('POST', '/auth/login', { identifier, password }),
  logout: () => request('POST', '/auth/logout'),

  // identity + inventory
  me: () => request('GET', '/v1/me'),
  capabilities: () => request('GET', '/v1/capabilities'),
  connections: (params) => request('GET', `/v1/connections${query(params)}`),
  syncConnection: (id) => request('POST', `/v1/connections/${id}/sync`, {}),
  revokeConnection: (id) => request('DELETE', `/v1/connections/${id}`),
  adAccounts: (params) => request('GET', `/v1/ad-accounts${query(params)}`),
  assets: (params) => request('GET', `/v1/assets${query(params)}`),

  // analytics
  dailyInsights: (params) => request('GET', `/v1/insights/daily${query(params)}`),
  adEntities: (params) => request('GET', `/v1/ad-entities${query(params)}`),

  // launcher
  launchAccounts: (params) => request('GET', `/v1/launch/accounts${query(params)}`),
  launchTemplates: (params) => request('GET', `/v1/launch/templates${query(params)}`),
  launchPreview: (payload) => request('POST', '/v1/launch/preview', payload),
  launch: (payload) => request('POST', '/v1/launch', payload),

  // posting
  batches: (params) => request('GET', `/v1/batches${query(params)}`),
  batch: (id) => request('GET', `/v1/batches/${id}`),
  batchResults: (id, params) => request('GET', `/v1/batches/${id}/results${query(params)}`),
  createBatch: (payload) => request('POST', '/v1/batches', payload),
  stopBatch: (id) => request('POST', `/v1/batches/${id}/stop`, {}),
  publishedObjects: (params) => request('GET', `/v1/published-objects${query(params)}`),
  media: (params) => request('GET', `/v1/media${query(params)}`),

  /** Uploads one file. The response carries a media_id, which a launch
   *  references through a media binding: the publisher then uploads it into
   *  each target ad account and substitutes that account's own hash. Image
   *  hashes are per-account, so a single shared hash would be wrong for every
   *  account but one. */
  uploadMedia: async (file, connectionID) => {
    const body = new FormData();
    body.append('file', file);
    if (connectionID) body.append('connection_id', connectionID);
    const response = await fetch('/v1/media', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'X-CSRF-Token': csrfToken() },
      body,
    });
    const text = await response.text();
    let payload = null;
    try { payload = JSON.parse(text); } catch { payload = null; }
    if (!response.ok) {
      const error = (payload && payload.error) || {};
      throw new ApiError(response.status, error.code, error.message, error.details);
    }
    return payload;
  },

  // campaigns and guards
  campaigns: (params) => request('GET', `/v1/campaigns${query(params)}`),
  pauseCampaign: (id) => request('POST', `/v1/campaigns/${id}/pause`, {}),
  resumeCampaign: (id) => request('POST', `/v1/campaigns/${id}/resume`, {}),
  setCampaignGuard: (id, payload) => request('POST', `/v1/campaigns/${id}/guard`, payload),
  accountStats: (id) => request('GET', `/v1/ad-accounts/${id}/stats`),
  guards: (params) => request('GET', `/v1/guards${query(params)}`),
  guard: (id) => request('GET', `/v1/guards/${id}`),
  updateGuard: (id, payload) => request('PATCH', `/v1/guards/${id}`, payload),
  enableGuard: (id) => request('POST', `/v1/guards/${id}/enable`, {}),
  disableGuard: (id) => request('POST', `/v1/guards/${id}/disable`, {}),

  // history
  jobs: (params) => request('GET', `/v1/jobs${query(params)}`),

  // settings
  apiKeys: () => request('GET', '/v1/api-keys'),
  createApiKey: (name) => request('POST', '/v1/api-keys', { name }),
  revokeApiKey: (id) => request('DELETE', `/v1/api-keys/${id}`),

  // admin, cross-tenant
  adminUsers: (params) => request('GET', `/v1/admin/users${query(params)}`),
  adminConnections: (params) => request('GET', `/v1/admin/connections${query(params)}`),
  adminAdAccounts: (params) => request('GET', `/v1/admin/ad-accounts${query(params)}`),
  adminDailyInsights: (params) => request('GET', `/v1/admin/insights/daily${query(params)}`),
  adminRateLimits: () => request('GET', '/v1/admin/rate-limits'),
};
