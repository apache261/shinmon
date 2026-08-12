import { w2popup } from '../../vendor/w2ui/w2ui.es6.min.js?v=20260730';
import { MODULE_HELP } from './help-content.js';

let sequence = 0;

function appendList(host, heading, items, ordered = false) {
  const title = document.createElement('h3'); title.textContent = heading;
  const list = document.createElement(ordered ? 'ol' : 'ul');
  for (const item of items) { const entry = document.createElement('li'); entry.textContent = item; list.append(entry); }
  host.append(title, list);
}

export function showModuleHelp(moduleName) {
  const content = MODULE_HELP[moduleName];
  if (!content) throw new Error(`Unknown help module: ${moduleName}`);
  const hostID = `shinmon_help_${++sequence}`;
  const width = Math.min(760, Math.max(300, window.innerWidth - 24));
  const height = Math.min(650, Math.max(300, window.innerHeight - 24));
  w2popup.open({
    title: content.title,
    width,
    height,
    modal: true,
    showClose: true,
    showMax: true,
    body: `<div id="${hostID}" class="module-help"></div>`,
    actions: { Close() { w2popup.close(); } },
    onOpen(event) {
      event.onComplete = () => {
        const host = document.querySelector(`#${hostID}`);
        const introduction = document.createElement('p'); introduction.className = 'module-help-introduction'; introduction.textContent = content.introduction;
        host.append(introduction);
        appendList(host, 'How to use this module', content.steps, true);
        appendList(host, 'Good to know', content.tips);
      };
    },
  });
}
