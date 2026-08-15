import { w2layout, w2sidebar, w2toolbar, w2ui } from '../../vendor/w2ui/w2ui.es6.min.js?v=20260730';
import { navigate, currentRoute } from '../router.js';

const nodes = [
  { id: '/overview', text: 'Overview', icon: 'w2ui-icon-info' },
  { id: '/services', text: 'Services', icon: 'w2ui-icon-columns' },
  { id: '/ports', text: 'Ports & listeners', icon: 'w2ui-icon-settings' },
  { id: '/consumers', text: 'Consumers & keys', icon: 'w2ui-icon-user' },
  { id: '/configurations', text: 'Configurations', icon: 'w2ui-icon-check' },
  { id: '/gateways', text: 'Gateway health', icon: 'w2ui-icon-search' },
  { id: '/audit', text: 'Audit', icon: 'w2ui-icon-page' },
  { id: '/help', text: 'Help', icon: 'w2ui-icon-info' },
];

window.__shinmonDiagnostics = Object.freeze({ w2uiObjectCount: () => Object.keys(w2ui).length });

export function createAppLayout({ onLogout, onRefresh }) {
  for (const name of ['shinmon_layout', 'shinmon_sidebar', 'shinmon_toolbar']) w2ui[name]?.destroy();
  const layout = new w2layout({ name: 'shinmon_layout', panels: [{ type: 'top', size: 58 }, { type: 'left', size: 220, resizable: true }, { type: 'main' }] });
  layout.render('#app');
  const sidebar = new w2sidebar({ name: 'shinmon_sidebar', nodes, onClick(event) { navigate(event.target ?? event.detail?.target); } });
  const toolbar = new w2toolbar({ name: 'shinmon_toolbar', items: [{ type: 'html', id: 'brand', html: '<div class="app-brand"><img class="brand-logo" src="/assets/shinmon-logo.svg" alt=""><div>Shinmon<small>API Management Platform</small></div></div>' }, { type: 'spacer' }, { type: 'button', id: 'refresh', text: 'Refresh', icon: 'w2ui-icon-reload' }, { type: 'button', id: 'logout', text: 'Logout', icon: 'w2ui-icon-cross' }], onClick(event) { const id = event.target ?? event.detail?.target; if (id === 'logout') onLogout(); if (id === 'refresh') onRefresh(); } });
  layout.html('left', sidebar); layout.html('top', toolbar); sidebar.select(currentRoute());
  const selectRoute = () => sidebar.select(currentRoute());
  window.addEventListener('hashchange', selectRoute);
  return { main() { return layout.el('main'); }, destroy() { window.removeEventListener('hashchange', selectRoute); for (const name of ['shinmon_layout', 'shinmon_sidebar', 'shinmon_toolbar']) w2ui[name]?.destroy(); } };
}
