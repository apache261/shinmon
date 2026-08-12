import { api } from '../api-client.js';
import { mountView } from '../components/view-shell.js';
import { createGrid } from '../components/data-grid.js';
import { notify } from '../components/notification.js';
import { formatDate, statusHTML } from '../utils/formatting.js';

export async function render() { const shell = mountView('Gateway health', 'Replica readiness and loaded configuration versions.', [], 'gateways'); shell.content.innerHTML = '<section class="panel"><div id="gateways-grid" class="grid-host"></div></section>'; let grid; try { const items = await api.get('/gateway-instances'); grid = createGrid({ name: 'gateways_grid', box: '#gateways-grid', items, columns: [{ field: 'id', text: 'Instance', size: '26%' }, { field: 'address', text: 'Address', size: '24%' }, { field: 'loadedConfigurationVersion', text: 'Configuration', size: '18%' }, { field: 'ready', text: 'State', size: '14%', render: (record) => statusHTML(record.ready ? 'ready' : 'disabled') }, { field: 'lastSeenAt', text: 'Last seen', size: '18%', render: (record) => formatDate(record.lastSeenAt) }] }); } catch (error) { notify(error.message, 'error'); } return () => grid?.destroy(); }
