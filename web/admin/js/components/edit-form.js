import { w2form, w2popup, w2ui } from '../../vendor/w2ui/w2ui.es6.min.js?v=20260730';

let sequence = 0;
export function showForm({ title, fields, record = {}, width = 720, validate, submitLabel = 'Save' }) {
  return new Promise((resolve, reject) => {
    const name = `shinmon_form_${++sequence}`;
    let closeValue = null; let closeRequested = false; let completed = false;
    const finish = () => { if (completed) return; completed = true; if (w2ui[name]) w2ui[name].destroy(); resolve(closeValue); };
    const close = (value) => { if (closeRequested) return; closeRequested = true; closeValue = value; w2popup.close(); };
    const form = new w2form({ name, fields, record: { ...record }, actions: {
      [submitLabel]() { const errors = validate?.(this.record) || []; if (errors.length) { this.error(errors[0]); return; } close({ ...this.record }); },
      Cancel() { close(null); },
    }});
    try {
      const helpRows = fields.filter((field) => field.html?.text).length;
      const popupWidth = Math.min(width, Math.max(300, window.innerWidth - 24));
      const desiredHeight = 210 + fields.length * 68 + helpRows * 38;
      const popupHeight = Math.min(Math.max(300, desiredHeight), Math.max(300, window.innerHeight - 24));
      w2popup.open({ title, width: popupWidth, height: popupHeight, modal: true, showClose: true, showMax: true, body: `<div id="${name}_host" class="form-host"></div>`, onOpen(event) { event.onComplete = () => form.render(`#${name}_host`); }, onClose(event) { closeRequested = true; event.onComplete = finish; } });
    } catch (error) { if (w2ui[name]) w2ui[name].destroy(); completed = true; reject(error); }
  });
}
