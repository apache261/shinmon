import { api } from '../api-client.js';
import { mountView } from '../components/view-shell.js';
import { createGrid } from '../components/data-grid.js';
import { confirmAction } from '../components/confirm-dialog.js';
import { notify } from '../components/notification.js';
import { formatDate, statusHTML } from '../utils/formatting.js';
import { approvalLabel, configurationActions } from '../utils/configuration-workflow.js';
import { showConfigurationDetails } from '../components/configuration-details-dialog.js';

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

    <section class="panel configuration-history" aria-labelledby="snapshot-history-title">
      <div class="section-heading compact-heading configuration-history-heading">
        <div>
          <p class="section-kicker">Immutable history</p>
          <h2 id="snapshot-history-title">Snapshots</h2>
        </div>
        <div class="configuration-history-actions">
          <p>Select a version to inspect its contents and available action.</p>
          <button id="view-configuration-details" type="button" class="secondary-button" disabled>View details</button>
        </div>
      </div>
      <div id="configurations-grid" class="grid-host"></div>
    </section>`;

  const elements = {
    active: document.querySelector('#active-configuration'),
    activeMeta: document.querySelector('#active-configuration-meta'),
    drafts: document.querySelector('#draft-count'),
    ready: document.querySelector('#ready-count'),
    viewDetails: document.querySelector('#view-configuration-details'),
  };

  elements.viewDetails.addEventListener('click', openDetails);

  function updateSummary() {
    const active = items.find((item) => item.status === 'active');
    elements.active.textContent = active ? `Version ${active.configurationVersion}` : 'None';
    elements.activeMeta.textContent = active ? `Activated ${formatDate(active.activatedAt)}.` : 'No snapshot has been activated.';
    elements.drafts.textContent = String(items.filter((item) => item.status === 'draft').length);
    elements.ready.textContent = String(items.filter((item) => configurationActions(item).activate).length);
  }

  function updateSelection() {
    elements.viewDetails.disabled = !selected;
    elements.viewDetails.textContent = selected ? `View version ${selected.configurationVersion} details` : 'View details';
  }

  function openDetails() {
    if (!selected) return;
    showConfigurationDetails(selected, { onValidate: validate, onApprove: approve, onActivate: activate, onRollback: rollback });
  }

  async function load(preferredVersion = selected?.configurationVersion) {
    items = await api.get('/configurations');
    selected = items.find((item) => item.configurationVersion === preferredVersion) || null;
    updateSummary();
    updateSelection();
    const rows = items.map((item) => ({ ...item, approvalProgress: approvalLabel(item), sourceLabel: item.sourceVersionId ? `Restored from ${item.sourceVersionId}` : 'Original' }));
    if (grid) {
      grid.update(rows);
      if (selected) grid.select(selected.configurationVersion);
    }
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
          updateSelection();
        },
      });
      if (selected) grid.select(selected.configurationVersion);
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
