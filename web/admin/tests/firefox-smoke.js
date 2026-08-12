import assert from 'node:assert/strict';
import net from 'node:net';

const token = process.env.SHINMON_DASHBOARD_TEST_TOKEN;
assert.ok(token?.length >= 32, 'SHINMON_DASHBOARD_TEST_TOKEN is required');

const socket = net.createConnection(2828, '127.0.0.1');
let buffer = Buffer.alloc(0);
let sequence = 0;
const pending = new Map();
await new Promise((resolve, reject) => { socket.once('connect', resolve); socket.once('error', reject); });

socket.on('data', (chunk) => {
  buffer = Buffer.concat([buffer, chunk]);
  while (buffer.length) {
    const separator = buffer.indexOf(58);
    if (separator < 0) return;
    const length = Number(buffer.subarray(0, separator).toString('ascii'));
    if (!Number.isInteger(length) || buffer.length < separator + 1 + length) return;
    const message = JSON.parse(buffer.subarray(separator + 1, separator + 1 + length).toString('utf8'));
    buffer = buffer.subarray(separator + 1 + length);
    if (!Array.isArray(message)) continue;
    const request = pending.get(message[1]);
    if (!request) continue;
    pending.delete(message[1]);
    if (message[2]) request.reject(new Error(`${message[2].error}: ${message[2].message}`));
    else request.resolve(message[3]);
  }
});

function command(name, params = {}) {
  const id = ++sequence;
  const payload = Buffer.from(JSON.stringify([0, id, name, params]));
  socket.write(Buffer.concat([Buffer.from(`${payload.length}:`), payload]));
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
}

async function execute(script) {
  const result = await command('WebDriver:ExecuteScript', { script, args: [], newSandbox: false, sandbox: 'default', line: 0, filename: 'shinmon-firefox-smoke.js' });
  return result?.value;
}

async function waitFor(script, timeout = 15000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await execute(script)) return;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Firefox timed out waiting for ${script}`);
}

await command('WebDriver:NewSession', { capabilities: { alwaysMatch: { acceptInsecureCerts: true } } });
await command('WebDriver:Navigate', { url: 'http://127.0.0.1:4042/#/overview' });
await waitFor("return document.documentElement.dataset.appReady === 'true'");
await execute(`document.querySelector('#admin-token').value=${JSON.stringify(token)}; document.querySelector('#admin-actor').value='firefox-smoke'; document.querySelector('#login-form').requestSubmit(); return true`);
await waitFor("return document.querySelector('#login-screen').hidden && document.querySelector('.view h1')?.textContent === 'Overview'");
const routes = [
  ['/services', 'Services'], ['/ports', 'Ports & listeners'],
  ['/consumers', 'Consumers & keys'], ['/configurations', 'Configurations'],
  ['/gateways', 'Gateway health'], ['/audit', 'Audit'],
];
for (const [route, heading] of routes) {
  await execute(`location.hash=${JSON.stringify(`#${route}`)}; return true`);
  await waitFor(`return document.querySelector('.view h1')?.textContent === ${JSON.stringify(heading)}`);
}
console.log(`firefox routes passed: Overview, ${routes.map(([, heading]) => heading).join(', ')}`);
await command('Marionette:Quit', { flags: ['eForceQuit'] }).catch(() => {});
socket.end();
