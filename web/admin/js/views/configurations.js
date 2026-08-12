import { api } from '../api-client.js';
import { mountView } from '../components/view-shell.js';
import { createGrid } from '../components/data-grid.js';
import { confirmAction } from '../components/confirm-dialog.js';
import { notify } from '../components/notification.js';
import { formatDate, statusHTML } from '../utils/formatting.js';
import { approvalLabel, configurationActions, configurationGuidance } from '../utils/configuration-workflow.js';

export async function render() {
  let grid;
  let selected = null;
  let items = [];
  const shell = mountView(
    'Configurations',
    'Review and publish immutable snapshots of your services, listeners, access rules, and traffic policies.',
    [{ label: 'Create snapshot', primary: true, onClick: createDraft }],
    'configurations',
  );

  shell.content.innerHTML = `
    <section class="configuration-summary" aria-label="Configuration summary">
      <article class="summary-card summary-card-live">
        <span class="summary-label">Live configuration</span>
        <strong id="active-configuration">None</strong>
        <small id="active-configuration-meta">No snapshot has been activated.</small>
      </article>
      <article class="summary-card">
        <span class="summary-label">Draft snapshots</span>
        <strong id="draft-count">0</strong>
        <small>Captured changes waiting for validation.</small>
      </article>
      <article class="summary-card">
        <span class="summary-label">Ready to publish</span>
        <strong id="ready-count">0</strong>
        <small>Validated snapshots with required approvals.</small>
      </article>
    </section>

    <section class="panel workflow-panel" aria-labelledby="publishing-workflow-title">
      <div class="section-heading">
        <div>
          <p class="section-kicker">Safe publishing</p>
          <h2 id="publishing-workflow-title">How changes reach the gateways</h2>
        </div>
        <p>Configuration snapshots are immutable. Each activation publishes one complete, validated state atomically.</p>
      </div>
      <ol class="workflow-steps">
        <li><span>1</span><div><strong>Prepare</strong><small>Make service, listener, client, or policy changes.</small></div></li>
        <li><span>2</span><div><strong>Capture</strong><small>Create a snapshot of the current management state.</small></div></li>
        <li><span>3</span><div><strong>Validate</strong><small>Check all references and enabled upstreams.</small></div></li>
        <li><span>4</span><div><strong>Approve</strong><small>Collect independent approval when required.</small></div></li>
        <li><span>5</span><div><strong>Activate</strong><small>Publish atomically to every gateway replica.</small></div></li>
      </ol>
    </section>

    <div class="configuration-layout">
      <section class="panel configuration-history" aria-labelledby="snapshot-history-title">
        <div class="section-heading compact-heading">
          <div>
            <p class="section-kicker">Immutable history</p>
            <h2 id="snapshot-history-title">Snapshots</h2>
          </div>
          <p>Select a row to review its state and next action.</p>
        </div>
        <div id="configurations-grid" class="grid-host"></div>
      </section>

      <aside class="panel configuration-inspector" aria-labelledby="snapshot-details-title">
        <p class="section-kicker">Selected snapshot</p>
        <h2 id="snapshot-details-title">Details</h2>
        <div id="configuration-empty" class="selection-empty">Select a snapshot from the history to continue.</div>
        <div id="configuration-details" hidden>
          <div class="configuration-identity">
            <strong id="selected-version">—</strong>
            <span id="selected-status"></span>
          </div>
          <dl class="detail-list">
            <div><dt>Created by</dt><dd id="selected-creator">—</dd></div>
            <div><dt>Created</dt><dd id="selected-created">—</dd></div>
            <div><dt>Validated</dt><dd id="selected-validated">—</dd></div>
            <div><dt>Activated</dt><dd id="selected-activated">—</dd></div>
            <div><dt>Approvals</dt><dd id="selected-approvals">—</dd></div>
            <div><dt>Restored from</dt><dd id="selected-source">—</dd></div>
          </dl>
          <div class="snapshot-scope" aria-label="Snapshot contents">
            <span><strong id="selected-services">0</strong> services</span>
            <span><strong id="selected-versions">0</strong> versions</span>
            <span><strong id="selected-upstreams">0</strong> upstreams</span>
            <span><strong id="selected-listeners">0</strong> listeners</span>
          </div>
          <div class="next-action-card">
            <span>Recommended next step</span>
            <p id="configuration-guidance"></p>
          </div>
          <div class="inspector-actions" aria-label="Snapshot actions">
            <button id="validate-configuration" type="button">Validate draft</button>
            <button id="approve-configuration" type="button">Record approval</button>
            <button id="activate-configuration" type="button" class="primary-button">Activate snapshot</button>
            <button id="rollback-configuration" type="button">Restore this version</button>
          </div>
        </div>
      </aside>
    </div>`;

  const elements = {
    active: document.querySelector('#active-configuration'),
    activeMeta: document.querySelector('#active-configuration-meta'),
    drafts: document.querySelector('#draft-count'),
    ready: document.querySelector('#ready-count'),
    empty: document.querySelector('#configuration-empty'),
    details: document.querySelector('#configuration-details'),
    version: document.querySelector('#selected-version'),
    status: document.querySelector('#selected-status'),
    creator: document.querySelector('#selected-creator'),
    created: document.querySelector('#selected-created'),
    validated: document.querySelector('#selected-validated'),
    activated: document.querySelector('#selected-activated'),
    approvals: document.querySelector('#selected-approvals'),
    source: document.querySelector('#selected-source'),
    services: document.querySelector('#selected-services'),
    versions: document.querySelector('#selected-versions'),
    upstreams: document.querySelector('#selected-upstreams'),
    listeners: document.querySelector('#selected-listeners'),
    guidance: document.querySelector('#configuration-guidance'),
    validate: document.querySelector('#validate-configuration'),
    approve: document.querySelector('#approve-configuration'),
    activate: document.querySelector('#activate-configuration'),
    rollback: document.querySelector('#rollback-configuration'),
  };

  elements.validate.addEventListener('click', validate);
  elements.approve.addEventListener('click', approve);
  elements.activate.addEventListener('click', activate);
  elements.rollback.addEventListener('click', rollback);

  function updateSummary() {
    const active = items.find((item) => item.status === 'active');
    elements.active.textContent = active ? `Version ${active.configurationVersion}` : 'None';
    elements.activeMeta.textContent = active ? `Activated ${formatDate(active.activatedAt)}.` : 'No snapshot has been activated.';
    elements.drafts.textContent = String(items.filter((item) => item.status === 'draft').length);
    elements.ready.textContent = String(items.filter((item) => configurationActions(item).activate).length);
  }

  function updateInspector() {
    elements.empty.hidden = Boolean(selected);
    elements.details.hidden = !selected;
    if (!selected) return;
    const actions = configurationActions(selected);
    elements.version.textContent = `Version ${selected.configurationVersion}`;
    elements.status.innerHTML = statusHTML(selected.status);
    elements.creator.textContent = selected.createdBy || '—';
    elements.created.textContent = formatDate(selected.createdAt);
    elements.validated.textContent = formatDate(selected.validatedAt);
    elements.activated.textContent = formatDate(selected.activatedAt);
    elements.approvals.textContent = approvalLabel(selected);
    elements.source.textContent = selected.sourceVersionId ? `Version ${selected.sourceVersionId}` : 'Original snapshot';
    elements.services.textContent = String(selected.serviceCount || 0);
    elements.versions.textContent = String(selected.versionCount || 0);
    elements.upstreams.textContent = String(selected.upstreamCount || 0);
    elements.listeners.textContent = String(selected.listenerCount || 0);
    elements.guidance.textContent = configurationGuidance(selected);
    elements.validate.hidden = !actions.validate;
    elements.approve.hidden = !actions.approve;
    elements.activate.hidden = !actions.activate;
    elements.rollback.hidden = !actions.rollback;
  }

  async function load(preferredVersion = selected?.configurationVersion) {
    items = await api.get('/configurations');
    selected = items.find((item) => item.configurationVersion === preferredVersion) || null;
    updateSummary();
    updateInspector();
    const rows = items.map((item) => ({ ...item, approvalProgress: approvalLabel(item), sourceLabel: item.sourceVersionId ? `Restored from ${item.sourceVersionId}` : 'Original' }));
    if (grid) grid.update(rows);
    else {
      grid = createGrid({
        name: 'configurations_grid',
        box: '#configurations-grid',
        items: rows,
        columns: [
          { field: 'configurationVersion', text: 'Version', size: '13%', render: (record) => `#${record.configurationVersion}` },
          { field: 'status', text: 'State', size: '18%', render: (record) => statusHTML(record.status) },
          { field: 'approvalProgress', text: 'Approvals', size: '18%' },
          { field: 'createdBy', text: 'Created by', size: '22%' },
          { field: 'createdAt', text: 'Created', size: '29%', render: (record) => formatDate(record.createdAt) },
        ],
        onSelect: (record) => {
          selected = items.find((item) => item.configurationVersion === record.configurationVersion) || record;
          updateInspector();
        },
      });
    }
  }

  async function createDraft() {
    const created = await api.post('/configurations', {});
    notify(`Snapshot ${created.configurationVersion} created. Validate it when your changes are ready.`, 'success', 6000);
    await load(created.configurationVersion);
  }

  async function validate() {
    if (!configurationActions(selected).validate) return;
    await api.post(`/configurations/${selected.configurationVersion}/validate`, {});
    notify(`Snapshot ${selected.configurationVersion} passed validation.`, 'success');
    await load(selected.configurationVersion);
  }

  async function approve() {
    if (!configurationActions(selected).approve) return;
    try {
      await api.post(`/configurations/${selected.configurationVersion}/approve`, {});
      notify(`Approval recorded for snapshot ${selected.configurationVersion}.`, 'success');
      await load(selected.configurationVersion);
    } catch (error) {
      notify(error.status === 409 ? 'Approval must come from a different administrator and can only be recorded once.' : error.message, 'error', 6000);
    }
  }

  async function activate() {
    if (!configurationActions(selected).activate) return;
    if (!await confirmAction(`Publish snapshot ${selected.configurationVersion} to all gateway replicas?`)) return;
    const active = items.find((item) => item.status === 'active');
    await api.post(`/configurations/${selected.configurationVersion}/activate`, active ? { expectedActiveVersion: active.configurationVersion } : {});
    notify(`Snapshot ${selected.configurationVersion} is now live.`, 'success');
    await load(selected.configurationVersion);
  }

  async function rollback() {
    if (!configurationActions(selected).rollback) return;
    if (!await confirmAction(`Restore snapshot ${selected.configurationVersion} as a new live version?`)) return;
    const restored = await api.post(`/configurations/${selected.configurationVersion}/rollback`, {});
    notify(`Snapshot ${selected.configurationVersion} restored as version ${restored.configurationVersion}.`, 'success');
    await load(restored.configurationVersion);
  }

  try { await load(); } catch (error) { notify(error.message, 'error'); }
  return () => grid?.destroy();
}
