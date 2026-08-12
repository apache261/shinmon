const state = { environment: 'development', permissions: [], reference: new Map() };
const listeners = new Set();

export function getState() { return { ...state, reference: state.reference }; }
export function setState(patch) { Object.assign(state, patch); for (const listener of listeners) listener(getState()); }
export function subscribe(listener) { listeners.add(listener); return () => listeners.delete(listener); }
export function clearState() { state.environment = 'development'; state.permissions = []; state.reference.clear(); setState({}); }
