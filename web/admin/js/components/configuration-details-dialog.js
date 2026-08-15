import { w2popup } from '../../vendor/w2ui/w2ui.es6.min.js?v=20260730';
import { formatDate, statusHTML } from '../utils/formatting.js';
import { approvalLabel, configurationActions, configurationGuidance } from '../utils/configuration-workflow.js';

let sequence = 0;

function detailRow(label, value) {
  const row = document.createElement('div'); const term = document.createElement('dt'); const detail = document.createElement('dd');
  term.textContent = label; detail.textContent = value; row.append(term, detail); return row;
}

function scopeItem(count, label) {
  const item = document.createElement('span'); const value = document.createElement('strong'); value.textContent = String(count || 0); item.append(value, ` ${label}`); return item;
}

export function showConfigurationDetails(configuration, handlers = {}) {
  const hostID = `shinmon_configuration_details_${++sequence}`;
  const width = Math.min(760, Math.max(300, window.innerWidth - 24));
  const height = Math.min(720, Math.max(360, window.innerHeight - 24));
  let pendingAction = null;
  w2popup.open({
    title: `Snapshot ${configuration.configurationVersion} details`,
    width,
    height,
    modal: true,
    showClose: true,
    showMax: true,
    body: `<div id="${hostID}" class="configuration-details-dialog"></div>`,
    actions: { Close() { w2popup.close(); } },
    onOpen(event) {
      event.onComplete = () => {
        const host = document.querySelector(`#${hostID}`);
        const identity = document.createElement('div'); identity.className = 'configuration-identity';
        const version = document.createElement('strong'); version.textContent = `Version ${configuration.configurationVersion}`;
        const status = document.createElement('span'); status.innerHTML = statusHTML(configuration.status); identity.append(version, status);

        const details = document.createElement('dl'); details.className = 'detail-list';
        details.append(
          detailRow('Created by', configuration.createdBy || '—'),
          detailRow('Created', formatDate(configuration.createdAt)),
          detailRow('Validated', formatDate(configuration.validatedAt)),
          detailRow('Activated', formatDate(configuration.activatedAt)),
          detailRow('Approvals', approvalLabel(configuration)),
          detailRow('Restored from', configuration.sourceVersionId ? `Version ${configuration.sourceVersionId}` : 'Original snapshot'),
        );

        const scope = document.createElement('div'); scope.className = 'snapshot-scope'; scope.setAttribute('aria-label', 'Snapshot contents');
        scope.append(scopeItem(configuration.serviceCount, 'services'), scopeItem(configuration.versionCount, 'versions'), scopeItem(configuration.upstreamCount, 'upstreams'), scopeItem(configuration.listenerCount, 'listeners'));

        const guidance = document.createElement('div'); guidance.className = 'next-action-card';
        const guidanceLabel = document.createElement('span'); guidanceLabel.textContent = 'Recommended next step';
        const guidanceText = document.createElement('p'); guidanceText.id = 'configuration-guidance'; guidanceText.textContent = configurationGuidance(configuration); guidance.append(guidanceLabel, guidanceText);

        const actionHost = document.createElement('div'); actionHost.className = 'configuration-dialog-actions'; actionHost.setAttribute('aria-label', 'Snapshot actions');
        const available = configurationActions(configuration);
        const actions = [
          ['validate', 'Validate draft', handlers.onValidate, false],
          ['approve', 'Record approval', handlers.onApprove, false],
          ['activate', 'Activate snapshot', handlers.onActivate, true],
          ['rollback', 'Restore this version', handlers.onRollback, false],
        ];
        actions.filter(([name]) => available[name]).forEach(([, label, handler, primary]) => {
          const button = document.createElement('button'); button.type = 'button'; button.textContent = label; if (primary) button.className = 'primary-button';
          button.addEventListener('click', () => { pendingAction = handler; w2popup.close(); }); actionHost.append(button);
        });
        host.append(identity, details, scope, guidance, actionHost);
      };
    },
    onClose(event) {
      event.onComplete = () => { if (pendingAction) void pendingAction(); };
    },
  });
}
