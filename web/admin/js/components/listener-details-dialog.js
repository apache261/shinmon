import { w2popup } from '../../vendor/w2ui/w2ui.es6.min.js?v=20260730';
import { notify } from './notification.js';

let sequence = 0;

function clientHost(hostname) {
  const value = String(hostname || '127.0.0.1');
  return value.includes(':') && !value.startsWith('[') ? `[${value}]` : value;
}

async function copyValue(value, label) {
  try {
    await navigator.clipboard.writeText(value);
    notify(`${label} copied.`, 'success');
  } catch {
    notify(`Could not copy ${label.toLowerCase()}. Select and copy it manually.`, 'error');
  }
}

function detailRow(label, value, copyLabel) {
  const row = document.createElement('div'); row.className = 'listener-detail-row';
  const heading = document.createElement('span'); heading.className = 'listener-detail-label'; heading.textContent = label;
  const content = document.createElement('code'); content.textContent = value;
  row.append(heading, content);
  if (copyLabel) {
    const button = document.createElement('button'); button.type = 'button'; button.textContent = 'Copy'; button.setAttribute('aria-label', `Copy ${copyLabel}`);
    button.addEventListener('click', () => copyValue(value, copyLabel)); row.append(button);
  }
  return row;
}

export function showListenerDetails({ listener, service, version, upstreams = [], hostname = window.location.hostname }) {
  const hostID = `shinmon_listener_details_${++sequence}`;
  const endpoint = `${window.location.protocol}//${clientHost(hostname)}:${listener.listenPort}`;
  const width = Math.min(800, Math.max(300, window.innerWidth - 24));
  const height = Math.min(680, Math.max(300, window.innerHeight - 24));
  w2popup.open({
    title: `Connection details — port ${listener.listenPort}`,
    width,
    height,
    modal: true,
    showClose: true,
    showMax: true,
    body: `<div id="${hostID}" class="listener-details"></div>`,
    actions: { Close() { w2popup.close(); } },
    onOpen(event) {
      event.onComplete = () => {
        const host = document.querySelector(`#${hostID}`);
        const introduction = document.createElement('p'); introduction.className = 'module-help-introduction'; introduction.textContent = 'Give authorized consumers the client-facing address and an issued API key. They must send the key in the X-API-Key request header. Target addresses are operational details and should not be distributed to clients.';
        host.append(introduction);
        if (!listener.configurationVersion || listener.status !== 'active') {
          const warning = document.createElement('p'); warning.className = 'listener-publication-warning'; warning.setAttribute('role', 'alert');
          warning.textContent = !listener.configurationVersion ? 'Not ready to distribute: create, validate, and activate a configuration containing this listener first.' : `Not ready to distribute: this listener is ${listener.status}.`;
          host.append(warning);
        }
        const clientTitle = document.createElement('h3'); clientTitle.textContent = 'Distribute to API clients';
        host.append(clientTitle, detailRow('Client address', endpoint, 'client address'), detailRow('Authentication header', 'X-API-Key: <issued-api-key>', 'authentication header'), detailRow('Required access', listener.requiredPermission || 'None'), detailRow('Unprotected route regex', listener.unprotectedRouteRegex || 'None'));
        const targetTitle = document.createElement('h3'); targetTitle.textContent = 'Routing target'; host.append(targetTitle);
        host.append(detailRow('API', service ? `${service.displayName || service.name} / ${version?.version || 'unknown version'}` : listener.serviceVersionId));
        if (upstreams.length === 0) {
          const empty = document.createElement('p'); empty.className = 'listener-target-empty'; empty.textContent = 'No upstream target is configured for this API version.'; host.append(empty);
        } else {
          upstreams.forEach((upstream, index) => host.append(detailRow(`Target ${index + 1}`, `${upstream.scheme || 'http'}:${'//'}${clientHost(upstream.address)}:${upstream.port}`, `target ${index + 1}`)));
        }
        const stateTitle = document.createElement('h3'); stateTitle.textContent = 'Listener state'; host.append(stateTitle, detailRow('Status', listener.status), detailRow('Published configuration', listener.configurationVersion || 'Not published'), detailRow('Listener ID', listener.listenerId));
      };
    },
  });
}
