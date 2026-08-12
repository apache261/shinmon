import { api } from '../api-client.js';
import { mountView } from '../components/view-shell.js';
import { loadingHTML } from '../components/loading-state.js';
import { errorHTML } from '../components/error-state.js';
import { metricCard } from '../components/metric-card.js';
import { createStatusChart } from '../components/traffic-chart.js';
import { createGrid } from '../components/data-grid.js';
import { formatDate } from '../utils/formatting.js';

export async function render() {
  const { content } = mountView('Overview', 'Configuration inventory and gateway readiness at a glance.', [], 'overview'); content.innerHTML = loadingHTML('Loading operational summary…'); let chart; let auditGrid;
  try {
    const [services, ports, gateways, audit] = await Promise.all([api.get('/services'), api.get('/ports'), api.get('/gateway-instances'), api.get('/audit-events?limit=8')]);
    content.innerHTML = ''; const metrics = document.createElement('div'); metrics.className = 'metric-grid'; metrics.append(metricCard('Services', services.length), metricCard('Active ports', ports.filter((item) => item.status === 'active').length), metricCard('Available ports', ports.filter((item) => item.status === 'available').length), metricCard('Ready gateways', gateways.filter((item) => item.ready).length, `${gateways.length} registered`));
    const split = document.createElement('div'); split.className = 'split-layout'; const chartPanel = document.createElement('section'); chartPanel.className = 'panel'; chartPanel.innerHTML = '<h2>Port inventory</h2><div class="chart-host"><canvas aria-label="Port status distribution"></canvas></div>'; const auditPanel = document.createElement('section'); auditPanel.className = 'panel'; auditPanel.innerHTML = '<h2>Recent changes</h2><div class="grid-host compact"></div>'; split.append(chartPanel, auditPanel); content.append(metrics, split);
    const counts = Object.groupBy ? Object.groupBy(ports, (item) => item.status) : ports.reduce((all, item) => ((all[item.status] ||= []).push(item), all), {}); const labels = Object.keys(counts); chart = createStatusChart(chartPanel.querySelector('canvas'), labels, labels.map((label) => counts[label].length));
    auditGrid = createGrid({ name: 'overview_audit_grid', box: auditPanel.querySelector('.grid-host'), items: audit, columns: [{ field: 'action', text: 'Action', size: '35%' }, { field: 'resourceType', text: 'Resource', size: '25%' }, { field: 'actor', text: 'Actor', size: '20%' }, { field: 'createdAt', text: 'Time', size: '20%', render: (record) => formatDate(record.createdAt) }] });
  } catch (error) { content.innerHTML = ''; content.append(errorHTML(error.message)); }
  return () => { chart?.destroy(); auditGrid?.destroy(); };
}
