const bus = new EventTarget();
export const on = (name, listener) => { bus.addEventListener(name, listener); return () => bus.removeEventListener(name, listener); };
export const emit = (name, detail) => bus.dispatchEvent(new CustomEvent(name, { detail }));
