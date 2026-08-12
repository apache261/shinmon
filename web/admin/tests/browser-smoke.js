import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import http from 'node:http';
import net from 'node:net';

const token = process.env.SHINMON_DASHBOARD_TEST_TOKEN;
assert.ok(token?.length >= 32, 'SHINMON_DASHBOARD_TEST_TOKEN is required');

function getJSON(url) {
  return new Promise((resolve, reject) => http.get(url, (response) => {
    let body = '';
    response.setEncoding('utf8');
    response.on('data', (chunk) => { body += chunk; });
    response.on('end', () => { try { resolve(JSON.parse(body)); } catch (error) { reject(error); } });
  }).on('error', reject));
}

const targets = await getJSON('http://127.0.0.1:9222/json');
const target = targets.find((item) => item.type === 'page');
assert.ok(target, 'Chrome page target not found');

let sequence = 0;
const pending = new Map();
const diagnostics = [];
function receive(message) {
  if (message.method === 'Runtime.exceptionThrown') diagnostics.push(message.params.exceptionDetails.exception?.description || message.params.exceptionDetails.text);
  if (message.method === 'Runtime.consoleAPICalled') diagnostics.push(message.params.args.map((item) => item.value ?? item.description).join(' '));
  if (!message.id) return;
  const request = pending.get(message.id);
  pending.delete(message.id);
  if (message.error) request.reject(new Error(message.error.message));
  else request.resolve(message.result);
}

function connectWebSocket(url) {
  const endpoint = new URL(url);
  return new Promise((resolve, reject) => {
    const socket = net.createConnection(Number(endpoint.port), endpoint.hostname);
    let buffer = Buffer.alloc(0);
    let connected = false;
    socket.on('connect', () => {
      const key = randomBytes(16).toString('base64');
      socket.write(`GET ${endpoint.pathname} HTTP/1.1\r\nHost: ${endpoint.host}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: ${key}\r\nSec-WebSocket-Version: 13\r\n\r\n`);
    });
    socket.on('error', reject);
    socket.on('data', (chunk) => {
      buffer = Buffer.concat([buffer, chunk]);
      if (!connected) {
        const headerEnd = buffer.indexOf('\r\n\r\n');
        if (headerEnd < 0) return;
        const header = buffer.subarray(0, headerEnd).toString('utf8');
        if (!header.startsWith('HTTP/1.1 101')) { reject(new Error(`WebSocket upgrade failed: ${header.split('\r\n')[0]}`)); return; }
        buffer = buffer.subarray(headerEnd + 4);
        connected = true;
        resolve({ send, close: () => socket.end() });
      }
      while (connected && buffer.length >= 2) {
        let offset = 2;
        let length = buffer[1] & 0x7f;
        if (length === 126) { if (buffer.length < 4) return; length = buffer.readUInt16BE(2); offset = 4; }
        else if (length === 127) { if (buffer.length < 10) return; length = Number(buffer.readBigUInt64BE(2)); offset = 10; }
        if (buffer.length < offset + length) return;
        const opcode = buffer[0] & 0x0f;
        const payload = buffer.subarray(offset, offset + length);
        buffer = buffer.subarray(offset + length);
        if (opcode === 1) receive(JSON.parse(payload.toString('utf8')));
      }
    });
    function send(value) {
      const payload = Buffer.from(value);
      const mask = randomBytes(4);
      let header;
      if (payload.length < 126) header = Buffer.from([0x81, 0x80 | payload.length]);
      else if (payload.length <= 65535) { header = Buffer.alloc(4); header[0] = 0x81; header[1] = 0xfe; header.writeUInt16BE(payload.length, 2); }
      else { header = Buffer.alloc(10); header[0] = 0x81; header[1] = 0xff; header.writeBigUInt64BE(BigInt(payload.length), 2); }
      const masked = Buffer.alloc(payload.length);
      for (let index = 0; index < payload.length; index += 1) masked[index] = payload[index] ^ mask[index % 4];
      socket.write(Buffer.concat([header, mask, masked]));
    }
  });
}

const socket = await connectWebSocket(target.webSocketDebuggerUrl);

function command(method, params = {}) {
  const id = ++sequence;
  socket.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
}

async function evaluate(expression) {
  const result = await command('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) {
    const description = result.exceptionDetails.exception?.description;
    throw new Error(description || result.exceptionDetails.text);
  }
  return result.result.value;
}

async function waitFor(expression, timeout = 10000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await evaluate(expression)) return;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const state = await evaluate("JSON.stringify({loginError: document.querySelector('#login-error')?.textContent, hidden: document.querySelector('#login-screen')?.hidden, heading: document.querySelector('.view h1')?.textContent})");
  throw new Error(`Timed out waiting for ${expression}; state=${state}; diagnostics=${diagnostics.join(' | ')}`);
}

await command('Page.enable');
await command('Runtime.enable');
await command('Network.enable');
await command('Network.setCacheDisabled', { cacheDisabled: true });
await command('Page.navigate', { url: `http://127.0.0.1:4042/?smoke=${Date.now()}#/audit` });
await waitFor("document.documentElement?.dataset.appReady === 'true'");
await waitFor("typeof window.__shinmonDiagnostics?.w2uiObjectCount === 'function'");
await evaluate(`document.querySelector('#admin-token').value = ${JSON.stringify(token)}; document.querySelector('#admin-actor').value = 'browser-smoke'; document.querySelector('#login-form').requestSubmit(); true`);
await waitFor("document.querySelector('#login-screen').hidden && document.querySelector('.view h1')?.textContent === 'Audit'");
await evaluate("import('/js/events.js').then(({ emit }) => emit('request:started', { path: '/smoke' })); true");
await waitFor("document.querySelector('#request-loader')?.hidden === false");
await evaluate("import('/js/events.js').then(({ emit }) => emit('request:finished', { path: '/smoke' })); true");
await waitFor("document.querySelector('#request-loader')?.hidden === true");

const routes = [
  ['/overview', 'Overview'], ['/services', 'Services'], ['/ports', 'Ports & listeners'],
  ['/consumers', 'Consumers & keys'], ['/configurations', 'Configurations'],
  ['/gateways', 'Gateway health'], ['/audit', 'Audit'],
];
const headings = [];
let maximumW2UIObjects = 0;
let firstCycleW2UIObjects = 0;
for (let cycle = 0; cycle < 5; cycle += 1) {
  for (const [route, expectedHeading] of routes) {
    await evaluate(`location.hash = ${JSON.stringify(`#${route}`)}; true`);
    await waitFor(`location.hash === ${JSON.stringify(`#${route}`)} && document.documentElement.dataset.renderedRoute === ${JSON.stringify(route)} && document.querySelector('.view h1')?.textContent === ${JSON.stringify(expectedHeading)}`);
    assert.equal(await evaluate("document.querySelector('.toast.error')?.textContent || ''"), '', `${route} displayed an error toast`);
    assert.equal(await evaluate("document.querySelector('.view-content .error-state')?.textContent || ''"), '', `${route} displayed an error state`);
    assert.equal(await evaluate("[...document.querySelectorAll('.view-header .view-actions button')].some((button) => button.textContent === 'Help')"), true, `${route} did not expose module help`);
    if (cycle === 0) headings.push(await evaluate("document.querySelector('.view h1').textContent"));
    assert.equal(await evaluate("document.querySelector('#login-screen').hidden"), true, `${route} ended the session`);
    const state = await evaluate("JSON.stringify({objects:window.__shinmonDiagnostics.w2uiObjectCount(),duplicateIds:[...document.querySelectorAll('[id]')].map(n=>n.id).filter((id,index,all)=>all.indexOf(id)!==index).length})");
    const parsed = JSON.parse(state);
    maximumW2UIObjects = Math.max(maximumW2UIObjects, parsed.objects);
    if (cycle === 0) firstCycleW2UIObjects = Math.max(firstCycleW2UIObjects, parsed.objects);
    else assert.ok(parsed.objects <= firstCycleW2UIObjects, `${route} grew the W2UI registry from ${firstCycleW2UIObjects} to ${parsed.objects}`);
    assert.equal(parsed.duplicateIds, 0, `${route} leaked duplicate DOM IDs`);
  }
}

assert.equal(new Set(headings).size, routes.length, 'routes did not render distinct views');
assert.ok(firstCycleW2UIObjects >= 3 && firstCycleW2UIObjects <= 20, `W2UI registry reached an unexpected baseline of ${firstCycleW2UIObjects} objects`);

await evaluate("location.hash = '#/configurations'; true");
await waitFor("document.documentElement.dataset.renderedRoute === '/configurations' && document.querySelectorAll('.workflow-steps li').length === 5");
assert.equal(await evaluate("document.querySelectorAll('.configuration-summary .summary-card').length"), 3, 'configuration summary is incomplete');
assert.equal(await evaluate("document.querySelector('.view-header .view-actions')?.textContent.includes('Validate')"), false, 'configuration lifecycle actions leaked back into the page header');
const configurationCount = await evaluate("document.querySelectorAll('#configurations-grid [recid]').length");
if (configurationCount > 0) {
  await evaluate("document.querySelector('#configurations-grid [recid]')?.click(); true");
  await waitFor("document.querySelector('#configuration-details')?.hidden === false && document.querySelector('#configuration-guidance')?.textContent.length > 20");
  assert.ok(await evaluate("document.querySelectorAll('.inspector-actions button:not([hidden])').length") <= 1, 'configuration inspector exposed conflicting lifecycle actions');
}

await evaluate("location.hash = '#/ports'; true");
await waitFor("document.documentElement.dataset.renderedRoute === '/ports' && document.querySelector('#ports-grid .data-grid-results')?.textContent.includes('of')");
assert.equal(await evaluate("document.querySelector('#ports-grid .data-grid-page')?.textContent"), 'Page 1 of 4', 'port inventory did not paginate its 100 records');
await evaluate("document.querySelector('#ports-grid .data-grid-page-actions button:last-child').click(); true");
await waitFor("document.querySelector('#ports-grid .data-grid-page')?.textContent === 'Page 2 of 4'");
await evaluate("const input=document.querySelector('#ports-grid input[type=search]'); input.value='4100'; input.dispatchEvent(new Event('input', { bubbles:true })); true");
await waitFor("document.querySelector('#ports-grid .data-grid-results')?.textContent === '1–1 of 1 (100 total)'");
assert.equal(await evaluate("document.querySelector('#ports-grid .data-grid-page')?.textContent"), 'Page 1 of 1', 'filtering did not reset pagination');
await evaluate("const select=document.querySelector('#ports-grid .data-grid-field select'); select.value='status'; select.dispatchEvent(new Event('change', { bubbles:true })); true");
await waitFor("document.querySelector('#ports-grid .data-grid-results')?.textContent === '0–0 of 0 (100 total)'");

const buttonStyle = JSON.parse(await evaluate("JSON.stringify((() => { const style=getComputedStyle(document.querySelector('#block-port')); return {background:style.backgroundColor,border:style.borderStyle,radius:style.borderRadius,color:style.color}; })())"));
assert.notEqual(buttonStyle.background, 'rgba(0, 0, 0, 0)', 'dashboard buttons retained the browser default transparent background');
assert.equal(buttonStyle.border, 'solid', 'dashboard buttons do not use the designed border');
assert.notEqual(buttonStyle.radius, '0px', 'dashboard buttons do not use the designed radius');

assert.equal(await evaluate("document.querySelector('#listener-details').disabled"), true, 'connection details action should require a selected listener');
const listenerCount = await evaluate("document.querySelectorAll('#listeners-grid [recid]').length");
if (listenerCount > 0) {
  await evaluate("document.querySelector('#listeners-grid [recid]')?.click(); true");
  await waitFor("document.querySelector('#listener-details').disabled === false");
  await evaluate("document.querySelector('#listener-details').click(); true");
  await waitFor("document.querySelector('#w2ui-popup .listener-details')?.textContent.includes('Client address') && document.querySelector('#w2ui-popup .listener-details')?.textContent.includes('Routing target')");
  assert.ok(await evaluate("document.querySelector('#w2ui-popup .listener-detail-row code')?.textContent.includes(':')"), 'connection details did not display a client-facing port');
  await evaluate("[...document.querySelectorAll('#w2ui-popup button')].find((button) => button.textContent.trim() === 'Close')?.click(); true");
  await waitFor("document.querySelector('#w2ui-popup') === null");
}

await evaluate("[...document.querySelectorAll('.view-header .view-actions button')].find((button) => button.textContent === 'Help')?.click(); true");
await waitFor("document.querySelector('#w2ui-popup .module-help')?.textContent.includes('How to use this module')");
assert.ok(await evaluate("document.querySelectorAll('#w2ui-popup .module-help ol li').length") >= 3, 'module help did not provide actionable steps');
assert.ok(await evaluate("document.querySelector('#w2ui-popup .w2ui-popup-max') !== null"), 'dialog did not expose a maximize control');
await evaluate("document.querySelector('#w2ui-popup .w2ui-popup-max .w2ui-eaction')?.click(); true");
await waitFor("document.querySelector('#w2ui-popup')?.getBoundingClientRect().width >= window.innerWidth - 20");
await evaluate("[...document.querySelectorAll('#w2ui-popup button')].find((button) => button.textContent.trim() === 'Close')?.click(); true");
await waitFor("document.querySelector('#w2ui-popup') === null");

await evaluate("location.hash = '#/consumers'; true");
await waitFor("document.documentElement.dataset.renderedRoute === '/consumers' && document.querySelector('.view h1')?.textContent === 'Consumers & keys'");
await evaluate("[...document.querySelectorAll('.view-header .view-actions button')].find((button) => button.textContent === 'New consumer')?.click(); true");
await waitFor("document.querySelector('#w2ui-popup input[name=permissions]')?._w2field != null");
assert.equal(await evaluate("document.querySelector('#w2ui-popup input[name=permissions]')._w2field.type"), 'enum', 'consumer permissions are not a multiple-choice list');
const consumerLabelStyle = JSON.parse(await evaluate("JSON.stringify((() => { const label=document.querySelector('#w2ui-popup .w2ui-field > label'); const style=getComputedStyle(label); return {width:label.getBoundingClientRect().width,whiteSpace:style.whiteSpace}; })())"));
assert.ok(consumerLabelStyle.width >= 170, `dialog label column shrank to ${consumerLabelStyle.width}px`);
assert.notEqual(consumerLabelStyle.whiteSpace, 'nowrap', 'dialog labels cannot wrap');
await evaluate("document.querySelector('#w2ui-popup button[name=Cancel]')?.click(); true");
await waitFor("document.querySelector('#w2ui-popup') === null");

await evaluate("location.hash = '#/ports'; true");
await waitFor("document.documentElement.dataset.renderedRoute === '/ports' && document.querySelector('.view h1')?.textContent === 'Ports & listeners'");
await evaluate("[...document.querySelectorAll('.view-header .view-actions button')].find((button) => button.textContent === 'Add access rule')?.click(); true");
await waitFor("document.querySelector('#w2ui-popup input[name=serviceVersionChoice]')?._w2field != null && document.querySelector('#w2ui-popup input[name=accessLevel]')?._w2field != null");
const closedPermissionHost = await evaluate("document.querySelector('#w2ui-popup .form-host').id");
assert.equal(await evaluate("document.querySelector('#w2ui-popup input[name=serviceVersionChoice]')._w2field.type"), 'list', 'access rule API choice is not a dropdown');
assert.equal(await evaluate("document.querySelector('#w2ui-popup input[name=accessLevel]')._w2field.options.items.length"), 4, 'access rule does not provide predefined friendly levels');
await evaluate("document.querySelector('#w2ui-popup .w2ui-popup-close .w2ui-eaction')?.click(); true");
await waitFor("document.querySelector('#w2ui-popup') === null");
await evaluate("[...document.querySelectorAll('.view-header .view-actions button')].find((button) => button.textContent === 'Allocate listener')?.click(); true");
await waitFor("document.querySelector('#w2ui-popup input[name=requiredPermission]')?._w2field != null || document.querySelector('#w2ui-popup input[name=accessLevel]')?._w2field != null");
assert.equal(await evaluate("document.querySelectorAll('#w2ui-popup .form-host').length"), 1, 'permission and allocation dialogs overlap');
assert.notEqual(await evaluate("document.querySelector('#w2ui-popup .form-host').id"), closedPermissionHost, 'closed permission form was reused by allocation');
const guidedPermission = await evaluate("document.querySelector('#w2ui-popup input[name=accessLevel]') !== null");
if (guidedPermission) {
  assert.equal(await evaluate("document.querySelector('#w2ui-popup input[name=accessLevel]')._w2field.options.items[0].text"), 'Use the API (recommended)', 'empty access catalog did not receive a friendly default');
} else {
  const listenerFieldTypes = JSON.parse(await evaluate("JSON.stringify(Object.fromEntries(['serviceVersionChoice','preferredPort','requiredPermission','allowedMethods','allowedContentTypes'].map((name) => [name, document.querySelector(`#w2ui-popup input[name=${name}]`)?._w2field?.type])))"));
  assert.deepEqual(listenerFieldTypes, { serviceVersionChoice: 'list', preferredPort: 'list', requiredPermission: 'list', allowedMethods: 'enum', allowedContentTypes: 'enum' });
  assert.ok(await evaluate("document.querySelector('#w2ui-popup input[name=requiredPermission]')._w2field.options.items.length") > 0, 'required permission dropdown is empty');
}
await evaluate("document.querySelector('#w2ui-popup button[name=Cancel]')?.click(); true");
await waitFor("document.querySelector('#w2ui-popup') === null");

await evaluate("location.hash = '#/consumers'; true");
await waitFor("document.documentElement.dataset.renderedRoute === '/consumers' && document.querySelector('#consumers-grid [recid]') !== null");
await evaluate("document.querySelector('#consumers-grid [recid]')?.click(); true");
await waitFor("document.querySelector('#issue-key')?.disabled === false");
await evaluate("document.querySelector('#issue-key').click(); true");
await waitFor("document.querySelector('#w2ui-popup input[name=name]') !== null && document.querySelector('#w2ui-popup input[name=expiresAt]') !== null");
assert.equal(await evaluate("document.querySelector('#w2ui-popup input[name=permissions]') === null"), true, 'key form still asks users to configure technical permissions');
await evaluate("document.querySelector('#w2ui-popup button[name=Cancel]')?.click(); true");
await waitFor("document.querySelector('#w2ui-popup') === null");

await evaluate("location.hash = '#/services'; true");
await waitFor("document.documentElement.dataset.renderedRoute === '/services' && document.querySelector('.view h1')?.textContent === 'Services'");
await evaluate("[...document.querySelectorAll('.view-header .view-actions button')].find((button) => button.textContent === 'New service')?.click(); true");
await waitFor("document.querySelector('#w2ui-popup input[name=name]') !== null");
await evaluate("document.querySelector('#w2ui-popup button[name=Cancel]')?.click(); true");
await waitFor("document.querySelector('#w2ui-popup') === null");

console.log(`browser routes passed: ${headings.join(', ')}; cycles=5; maxW2UI=${maximumW2UIObjects}`);
socket.close();
