import { showModuleHelp } from './help-dialog.js';

export function mountView(title, description, actions = [], helpModule = null) {
  const main = document.querySelector('#layout_shinmon_layout_panel_main .w2ui-panel-content') || document.querySelector('#app');
  main.innerHTML = '';
  const view = document.createElement('section'); view.className = 'view'; view.id = 'main-content'; view.tabIndex = -1;
  const header = document.createElement('header'); header.className = 'view-header';
  const copy = document.createElement('div'); const heading = document.createElement('h1'); heading.textContent = title; const paragraph = document.createElement('p'); paragraph.textContent = description; copy.append(heading, paragraph);
  const actionHost = document.createElement('div'); actionHost.className = 'view-actions';
  for (const action of actions) { const button = document.createElement('button'); button.type = 'button'; button.className = action.primary ? 'primary-button' : 'secondary-button'; button.textContent = action.label; button.addEventListener('click', action.onClick); actionHost.append(button); }
  if (helpModule) { const button = document.createElement('button'); button.type = 'button'; button.className = 'help-button'; button.textContent = 'Help'; button.setAttribute('aria-label', `Help for ${title}`); button.addEventListener('click', () => showModuleHelp(helpModule)); actionHost.append(button); }
  header.append(copy, actionHost); const content = document.createElement('div'); content.className = 'view-content'; view.append(header, content); main.append(view); view.focus();
  return { view, content, actionHost };
}
