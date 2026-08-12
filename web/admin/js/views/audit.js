import { api } from '../api-client.js';
import { mountView } from '../components/view-shell.js';
import { createGrid } from '../components/data-grid.js';
import { notify } from '../components/notification.js';
import { formatDate } from '../utils/formatting.js';

export async function render() { const shell = mountView('Audit', 'Immutable administrative actions recorded by PostgreSQL.', [], 'audit'); shell.content.innerHTML = '<section class="panel"><div id="audit-grid" class="grid-host"></div></section>'; let grid; try { const items = await api.get('/audit-events?limit=500'); grid = createGrid({ name: 'audit_grid', box: '#audit-grid', items, columns: [{ field: 'id', text: 'ID', size: '9%' }, { field: 'action', text: 'Action', size: '22%' }, { field: 'resourceType', text: 'Resource type', size: '16%' }, { field: 'resourceId', text: 'Resource ID', size: '20%' }, { field: 'actor', text: 'Actor', size: '15%' }, { field: 'createdAt', text: 'Time', size: '18%', render: (record) => formatDate(record.createdAt) }] }); } catch (error) { notify(error.message, 'error'); } return () => grid?.destroy(); }
