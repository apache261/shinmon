import { mountView } from '../components/view-shell.js';
export async function render() { const { content } = mountView('Not found', 'The requested dashboard view does not exist.'); content.innerHTML = '<div class="empty-state"><p>Use the navigation to choose a Shinmon dashboard view.</p></div>'; return () => {}; }
