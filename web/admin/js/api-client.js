import { API_BASE, REQUEST_TIMEOUT_MS } from './config.js';
import { authorizationHeaders, clearSession } from './auth.js';
import { emit } from './events.js';

export class APIError extends Error {
  constructor(message, status = 0, code = 'REQUEST_FAILED', details = null) { super(message); this.name = 'APIError'; this.status = status; this.code = code; this.details = details; }
}

function correlationID() { return globalThis.crypto?.randomUUID?.() || `dashboard-${Date.now()}-${Math.random().toString(16).slice(2)}`; }

async function request(path, options = {}) {
  emit('request:started', { path, method: options.method || 'GET' });
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), options.timeout ?? REQUEST_TIMEOUT_MS);
  const headers = { Accept: 'application/json', 'X-Correlation-ID': correlationID(), ...authorizationHeaders(), ...(options.headers || {}) };
  let body;
  if (options.body !== undefined) { headers['Content-Type'] = 'application/json'; body = JSON.stringify(options.body); }
  try {
    const response = await fetch(`${API_BASE}${path}`, { method: options.method || 'GET', headers, body, signal: controller.signal, credentials: 'same-origin' });
    const contentType = response.headers.get('content-type') || '';
    const payload = contentType.includes('application/json') ? await response.json() : null;
    if (!response.ok) {
      if (response.status === 401) clearSession();
      const message = payload?.error?.message || payload?.error || `Request failed (${response.status})`;
      throw new APIError(message, response.status, payload?.error?.code || 'REQUEST_FAILED', payload);
    }
    return payload;
  } catch (error) {
    if (error instanceof APIError) throw error;
    if (error.name === 'AbortError') throw new APIError('The request timed out.', 0, 'TIMEOUT');
    throw new APIError('The management service is unavailable.', 0, 'NETWORK_ERROR');
  } finally { clearTimeout(timeout); emit('request:finished', { path, method: options.method || 'GET' }); }
}

export const api = {
  get: (path, options) => request(path, options),
  post: (path, body = {}) => request(path, { method: 'POST', body }),
  patch: (path, body) => request(path, { method: 'PATCH', body }),
  delete: (path) => request(path, { method: 'DELETE' }),
};
