import { DEFAULT_ROUTE } from './config.js';

let routes = new Map();
let cleanup = null;
let started = false;
let renderGeneration = 0;

export function configureRouter(definitions) { routes = new Map(Object.entries(definitions)); }
export function currentRoute() { return (location.hash.slice(1) || DEFAULT_ROUTE).split('?')[0]; }
export function navigate(path) { location.hash = `#${path}`; }

export async function renderRoute() {
  const generation = ++renderGeneration;
  delete document.documentElement.dataset.renderedRoute;
  cleanup?.(); cleanup = null;
  const path = currentRoute();
  const loader = routes.get(path) || routes.get('/not-found');
  const module = await loader();
  const nextCleanup = await module.render({ path, navigate });
  if (generation !== renderGeneration) {
    nextCleanup?.();
    return;
  }
  cleanup = nextCleanup;
  document.documentElement.dataset.renderedRoute = path;
  window.dispatchEvent(new CustomEvent('shinmon:route-rendered', { detail: { path } }));
}

export function startRouter() {
  if (started) { void renderRoute(); return; }
  started = true;
  window.addEventListener('hashchange', renderRoute);
  if (!location.hash) navigate(DEFAULT_ROUTE); else void renderRoute();
}
