import { establishSession, clearSession, isAuthenticated } from './auth.js';
import { api } from './api-client.js';
import { on } from './events.js';
import { configureRouter, startRouter, renderRoute } from './router.js';
import { createAppLayout } from './components/app-layout.js';
import { initializeRequestLoader } from './components/request-loader.js';

let layout = null;
const loginScreen = document.querySelector('#login-screen');
const loginForm = document.querySelector('#login-form');
const loginError = document.querySelector('#login-error');
initializeRequestLoader();

configureRouter({
  '/overview': () => import('./views/overview.js'),
  '/services': () => import('./views/services.js'),
  '/ports': () => import('./views/ports.js'),
  '/consumers': () => import('./views/consumers.js'),
  '/keys': () => import('./views/consumers.js'),
  '/configurations': () => import('./views/configurations.js'),
  '/gateways': () => import('./views/gateways.js'),
  '/audit': () => import('./views/audit.js'),
  '/not-found': () => import('./views/not-found.js'),
});

function showLogin(message = '') { loginScreen.hidden = false; if (isAuthenticated()) clearSession(); layout?.destroy(); layout = null; document.querySelector('#app').innerHTML = ''; loginError.textContent = message; document.querySelector('#admin-token').value = ''; document.querySelector('#admin-token').focus(); }
function openApplication() { loginScreen.hidden = true; layout = createAppLayout({ onLogout: () => showLogin(), onRefresh: () => void renderRoute() }); startRouter(); }

loginForm.addEventListener('submit', async (event) => {
  event.preventDefault(); loginError.textContent = 'Connecting…'; const data = new FormData(loginForm); establishSession(String(data.get('token')), String(data.get('actor')));
  try { await api.get('/services'); loginError.textContent = ''; openApplication(); } catch (error) { showLogin(error.message); }
});

on('session:ended', () => { if (isAuthenticated()) return; if (loginScreen.hidden) showLogin('Your dashboard session ended.'); });
document.documentElement.dataset.appReady = 'true';
showLogin();
