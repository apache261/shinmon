export function filterGridRecords(items, columns, query, field = 'all') {
  const term = String(query || '').trim().toLocaleLowerCase();
  if (!term) return items.slice();
  const fields = field === 'all' ? columns.map((column) => column.field) : [field];
  return items.filter((item) => fields.some((name) => String(item[name] ?? '').toLocaleLowerCase().includes(term)));
}

export function paginateGridRecords(items, requestedPage, requestedPageSize) {
  const pageSize = Math.max(1, Number(requestedPageSize) || 25);
  const pageCount = Math.max(1, Math.ceil(items.length / pageSize));
  const page = Math.min(pageCount, Math.max(1, Number(requestedPage) || 1));
  const startIndex = (page - 1) * pageSize;
  return {
    items: items.slice(startIndex, startIndex + pageSize),
    page,
    pageCount,
    pageSize,
    start: items.length ? startIndex + 1 : 0,
    end: Math.min(startIndex + pageSize, items.length),
    total: items.length,
  };
}
