import { w2popup } from '../../vendor/w2ui/w2ui.es6.min.js?v=20260730';
import { escapeHTML } from '../utils/formatting.js';

export function showSecretOnce(secret) {
  const safe = escapeHTML(secret);
  const width = Math.min(760, Math.max(300, window.innerWidth - 24));
  const height = Math.min(340, Math.max(280, window.innerHeight - 24));
  w2popup.open({ title: 'API key — shown once', width, height, modal: true, showClose: true, showMax: true, body: `<div class="dialog-form"><p>Copy this key now. Shinmon cannot recover it after this dialog closes.</p><div class="secret-once" role="textbox" aria-label="Issued API key">${safe}</div></div>`, actions: { Close() { w2popup.close(); } } });
}
