export const required = (value) => String(value ?? '').trim().length > 0;
export const serviceName = (value) => /^[a-z][a-z0-9-]{1,62}$/.test(String(value));
export const versionName = (value) => {
  const version = String(value ?? '');
  return [...version].length >= 1 && [...version].length <= 128 && !/\s/u.test(version);
};
export const permissionName = (value) => /^[a-z][a-z0-9-]*:[a-zA-Z0-9.-]+:[a-z][a-z0-9-]*$/.test(String(value));
export const literalIP = (value) => /^((25[0-5]|2[0-4]\d|1?\d?\d)(\.|$)){4}$/.test(String(value)) || String(value).includes(':');
export const portNumber = (value) => Number.isInteger(Number(value)) && Number(value) >= 1 && Number(value) <= 65535;

export function routeRegex(value) {
  const pattern = String(value ?? '');
  if (pattern.length > 2048 || /[\0\r\n]/u.test(pattern)) return false;
  if (!pattern) return true;
  try { new RegExp(pattern, 'u'); return true; } catch { return false; }
}
