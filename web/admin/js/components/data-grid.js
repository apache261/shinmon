import { w2grid, w2ui } from '../../vendor/w2ui/w2ui.es6.min.js?v=20260730';
import { records, escapeHTML } from '../utils/formatting.js';
import { filterGridRecords, paginateGridRecords } from '../utils/grid-state.js';

export function createGrid({ name, box, columns, items = [], toolbar = null, onSelect = null }) {
  w2ui[name]?.destroy();
  const host = typeof box === 'string' ? document.querySelector(box) : box;
  if (!host) throw new Error(`Grid host for ${name} was not found.`);
  host.replaceChildren();
  host.classList.add('data-grid-shell');

  const controls = document.createElement('div'); controls.className = 'data-grid-controls';
  const filterLabel = document.createElement('label'); filterLabel.className = 'data-grid-filter';
  const filterText = document.createElement('span'); filterText.textContent = 'Filter';
  const filterInput = document.createElement('input'); filterInput.type = 'search'; filterInput.placeholder = 'Filter records…'; filterInput.setAttribute('aria-label', `Filter ${name.replaceAll('_', ' ')}`);
  filterLabel.append(filterText, filterInput);
  const fieldLabel = document.createElement('label'); fieldLabel.className = 'data-grid-field';
  const fieldText = document.createElement('span'); fieldText.textContent = 'Column';
  const fieldSelect = document.createElement('select'); fieldSelect.setAttribute('aria-label', `Filter column for ${name.replaceAll('_', ' ')}`);
  fieldSelect.append(new Option('All columns', 'all'));
  columns.filter((column) => column.field).forEach((column) => fieldSelect.append(new Option(column.text || column.field, column.field)));
  fieldLabel.append(fieldText, fieldSelect);
  const sizeLabel = document.createElement('label'); sizeLabel.className = 'data-grid-page-size';
  const sizeText = document.createElement('span'); sizeText.textContent = 'Rows';
  const sizeSelect = document.createElement('select'); sizeSelect.setAttribute('aria-label', `Rows per page for ${name.replaceAll('_', ' ')}`);
  [10, 25, 50, 100].forEach((size) => sizeSelect.append(new Option(String(size), String(size), false, size === 25)));
  sizeLabel.append(sizeText, sizeSelect);
  controls.append(filterLabel, fieldLabel, sizeLabel);

  const canvas = document.createElement('div'); canvas.className = 'data-grid-canvas';
  const pagination = document.createElement('div'); pagination.className = 'data-grid-pagination';
  const resultText = document.createElement('span'); resultText.className = 'data-grid-results'; resultText.setAttribute('aria-live', 'polite');
  const pageActions = document.createElement('div'); pageActions.className = 'data-grid-page-actions';
  const previous = document.createElement('button'); previous.type = 'button'; previous.textContent = 'Previous'; previous.setAttribute('aria-label', `Previous page of ${name.replaceAll('_', ' ')}`);
  const pageText = document.createElement('span'); pageText.className = 'data-grid-page'; pageText.setAttribute('aria-live', 'polite');
  const next = document.createElement('button'); next.type = 'button'; next.textContent = 'Next'; next.setAttribute('aria-label', `Next page of ${name.replaceAll('_', ' ')}`);
  pageActions.append(previous, pageText, next); pagination.append(resultText, pageActions);
  host.append(controls, canvas, pagination);

  const safeColumns = columns.map((column) => column.render ? column : { ...column, render(record) { return escapeHTML(record[column.field]); } });
  const options = { name, useLocalStorage: false, show: { toolbar: Boolean(toolbar), footer: false, toolbarReload: false, toolbarColumns: false, toolbarSearch: true }, columns: safeColumns, records: [], onClick(event) { const recid = event.recid ?? event.detail?.recid; const record = this.get(recid); onSelect?.(record, this); } };
  if (toolbar) options.toolbar = { items: toolbar.items || [], onClick: toolbar.onClick };
  const grid = new w2grid(options);
  grid.render(canvas);

  let sourceRecords = records(items); let page = 1;
  function refresh() {
    const filtered = filterGridRecords(sourceRecords, columns, filterInput.value, fieldSelect.value);
    const state = paginateGridRecords(filtered, page, sizeSelect.value);
    page = state.page;
    grid.selectNone(true); grid.records = state.items; grid.total = state.items.length; grid.refresh();
    const suffix = filtered.length === sourceRecords.length ? '' : ` (${sourceRecords.length} total)`;
    resultText.textContent = `${state.start}–${state.end} of ${state.total}${suffix}`;
    pageText.textContent = `Page ${state.page} of ${state.pageCount}`;
    previous.disabled = state.page <= 1; next.disabled = state.page >= state.pageCount;
  }
  filterInput.addEventListener('input', () => { page = 1; refresh(); });
  fieldSelect.addEventListener('change', () => { page = 1; refresh(); });
  sizeSelect.addEventListener('change', () => { page = 1; refresh(); });
  previous.addEventListener('click', () => { page -= 1; refresh(); });
  next.addEventListener('click', () => { page += 1; refresh(); });
  refresh();

  return { grid, update(nextItems) { sourceRecords = records(nextItems); page = 1; refresh(); }, select(recid) { grid.select(recid); }, destroy() { if (w2ui[name]) w2ui[name].destroy(); host.replaceChildren(); host.classList.remove('data-grid-shell'); } };
}
