import { emit } from './events.js';

let token = '';
let actor = '';

export function establishSession(nextToken, nextActor) { token = nextToken; actor = nextActor || 'dashboard-admin'; emit('session:started'); }
export function clearSession() { token = ''; actor = ''; emit('session:ended'); }
export function isAuthenticated() { return token.length > 0; }
export function authorizationHeaders() {
  if (!token) return {};
  return { Authorization: `Bearer ${token}`, 'X-Admin-Actor': actor };
}
