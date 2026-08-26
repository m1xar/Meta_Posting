// Presentation helpers.

export const el = (tag, attrs = {}, ...children) => {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === undefined || value === null || value === false) continue;
    if (key === 'class') node.className = value;
    else if (key === 'html') node.innerHTML = value;
    else if (key.startsWith('on') && typeof value === 'function') node.addEventListener(key.slice(2), value);
    else node.setAttribute(key, value === true ? '' : value);
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
};

export const clear = (node) => { while (node.firstChild) node.removeChild(node.firstChild); return node; };

const numberFormat = new Intl.NumberFormat('en-US');

export const int = (value) => numberFormat.format(Math.round(Number(value) || 0));

export const money = (value, currency) => {
  const amount = Number(value) || 0;
  try {
    return new Intl.NumberFormat('en-US', {
      style: 'currency', currency: currency || 'USD', maximumFractionDigits: 2,
    }).format(amount);
  } catch {
    return `${amount.toFixed(2)} ${currency || ''}`.trim();
  }
};

export const ratio = (value, digits = 2) => `${(Number(value) || 0).toFixed(digits)}%`;

/** Renders a value the server could not compute as absent rather than zero. */
export const orUnknown = (value, render) =>
  value === null || value === undefined ? '—' : render(value);

export const date = (value) => {
  if (!value) return '—';
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return '—';
  return parsed.toISOString().slice(0, 10);
};

export const dateTime = (value) => {
  if (!value) return '—';
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return '—';
  return parsed.toISOString().slice(0, 16).replace('T', ' ') + 'Z';
};

export const relative = (value) => {
  if (!value) return 'never';
  const then = new Date(value).getTime();
  if (Number.isNaN(then)) return 'never';
  const seconds = Math.round((Date.now() - then) / 1000);
  if (seconds < 60) return 'just now';
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
};

export const isoDaysAgo = (days) => {
  const day = new Date();
  day.setUTCDate(day.getUTCDate() - days);
  return day.toISOString().slice(0, 10);
};

/** Maps a domain status onto one of the four pill tones. */
export const tone = (status) => {
  const value = String(status || '').toLowerCase();
  if (['active', 'succeeded', 'ready', 'completed', 'connected'].includes(value)) return 'ok';
  if (['pending', 'running', 'processing', 'paused', 'partial'].includes(value)) return 'warn';
  if (['failed', 'dead', 'error', 'expired', 'revoked', 'disconnected', 'disabled'].includes(value)) return 'bad';
  return 'info';
};

export const pill = (text, toneName) =>
  el('span', { class: `pill ${toneName || tone(text)}` }, String(text || '—').toLowerCase());
