export function selectedValue(value) {
  if (value === null || value === undefined) return '';
  if (typeof value === 'object') return value.id ?? value.value ?? '';
  return value;
}

export function selectedValues(value) {
  if (!value) return [];
  const values = Array.isArray(value) ? value : [value];
  return values.map(selectedValue).map(String).filter(Boolean);
}

export function choice(id, text = id) {
  return { id: String(id), text: String(text) };
}

export function optionalISOString(value) {
  if (!value) return '';
  const date = value instanceof Date ? value : new Date(value);
  return Number.isNaN(date.getTime()) ? null : date.toISOString();
}
