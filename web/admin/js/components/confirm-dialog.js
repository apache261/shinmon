import { w2confirm } from '../../vendor/w2ui/w2ui.es6.min.js?v=20260730';
export function confirmAction(message) {
  return new Promise((resolve) => {
    try { w2confirm(message).yes(() => resolve(true)).no(() => resolve(false)); }
    catch { resolve(false); }
  });
}
