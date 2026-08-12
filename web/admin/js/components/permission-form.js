import { api } from '../api-client.js';
import { showForm } from './edit-form.js';
import { notify } from './notification.js';
import { choice, selectedValue } from '../utils/form-values.js';
import { ACCESS_LEVELS, buildPermissionName, friendlyPermissionName } from '../utils/permissions.js';
import { escapeHTML } from '../utils/formatting.js';

export async function createPermissionRule({ suggestedServiceVersion = '', title = 'Add access rule' } = {}) {
  try {
    const [services, catalog] = await Promise.all([api.get('/services'), api.get('/permissions')]);
    const versionSets = await Promise.all(services.map(async (service) => ({ service, versions: await api.get(`/services/${encodeURIComponent(service.name)}/versions`) })));
    const serviceVersions = versionSets.flatMap(({ service, versions }) => versions.map((version) => choice(`${service.name}/${version.version}`, `${escapeHTML(service.displayName || service.name)} — ${version.version}`)));
    if (serviceVersions.length === 0) { notify('Add a service version before creating an access rule.', 'error', 5000); return null; }
    const selectedServiceVersion = serviceVersions.find((item) => item.id === suggestedServiceVersion) || serviceVersions[0];
    const record = await showForm({ title, fields: [{ field: 'serviceVersionChoice', type: 'list', required: true, options: { items: serviceVersions }, html: { label: 'Which API?', text: '<small class="field-help">Choose the service and version this access applies to.</small>' } }, { field: 'accessLevel', type: 'list', required: true, options: { items: ACCESS_LEVELS }, html: { label: 'What can they do?', text: '<small class="field-help">Use the API is the normal choice for clients calling an endpoint.</small>' } }, { field: 'description', type: 'textarea', html: { label: 'Notes (optional)' } }], record: { serviceVersionChoice: selectedServiceVersion, accessLevel: ACCESS_LEVELS[0], description: '' }, submitLabel: 'Add access', validate: (item) => !selectedValue(item.serviceVersionChoice) ? ['Choose an API.'] : !selectedValue(item.accessLevel) ? ['Choose what the client can do.'] : [] });
    if (!record) return null;
    const name = buildPermissionName(selectedValue(record.serviceVersionChoice), selectedValue(record.accessLevel));
    const existing = catalog.find((permission) => permission.name === name);
    if (existing) { notify(`${friendlyPermissionName(name)} already exists and is ready to use.`, 'info', 5000); return existing; }
    const created = await api.post('/permissions', { name, description: String(record.description || '').trim() });
    notify('Access rule added.', 'success');
    return created;
  } catch (error) { notify(error.message, 'error'); return null; }
}
