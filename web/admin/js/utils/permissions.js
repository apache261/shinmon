import { choice } from './form-values.js';
import { escapeHTML } from './formatting.js';

export const ACCESS_LEVELS = Object.freeze([
  choice('invoke', 'Use the API (recommended)'),
  choice('read', 'View data'),
  choice('write', 'Create or change data'),
  choice('manage', 'Manage the API'),
]);

const ACTION_LABELS = Object.freeze({ invoke: 'Use', read: 'View', write: 'Change', manage: 'Manage' });

export function buildPermissionName(serviceVersion, action) {
  const [service, version] = String(serviceVersion || '').split('/', 2);
  return service && version && action ? `${service}:${version}:${action}` : '';
}

export function friendlyPermissionName(name) {
  const [service, version, action] = String(name || '').split(':', 3);
  if (!service || !version || !action) return String(name || 'Unknown access');
  const verb = ACTION_LABELS[action] || action.charAt(0).toUpperCase() + action.slice(1);
  return `${verb} ${service.replaceAll('-', ' ')} (${version})`;
}

export function permissionChoice(permission) {
  const friendly = friendlyPermissionName(permission.name);
  return choice(permission.name, permission.description ? `${friendly} — ${escapeHTML(permission.description)}` : friendly);
}
