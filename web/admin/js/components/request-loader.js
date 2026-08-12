import { on } from '../events.js';

export function initializeRequestLoader() {
  const loader = document.querySelector('#request-loader');
  let pending = 0;
  const refresh = () => { loader.hidden = pending === 0; loader.setAttribute('aria-hidden', String(pending === 0)); };
  const stopStarted = on('request:started', () => { pending += 1; refresh(); });
  const stopFinished = on('request:finished', () => { pending = Math.max(0, pending - 1); refresh(); });
  refresh();
  return () => { stopStarted(); stopFinished(); pending = 0; refresh(); };
}
