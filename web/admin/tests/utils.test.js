import test from 'node:test';
import assert from 'node:assert/strict';
import { escapeHTML, formatBytes, records, statusHTML } from '../js/utils/formatting.js';
import { filterGridRecords, paginateGridRecords } from '../js/utils/grid-state.js';
import { choice, optionalISOString, selectedValue, selectedValues } from '../js/utils/form-values.js';
import { MODULE_HELP } from '../js/components/help-content.js';
import { buildPermissionName, friendlyPermissionName, permissionChoice } from '../js/utils/permissions.js';
import { literalIP, permissionName, portNumber, required, routeRegex, serviceName, versionName } from '../js/utils/validation.js';
import { approvalLabel, configurationActions, configurationGuidance } from '../js/utils/configuration-workflow.js';

test('validation accepts management identifiers and rejects malformed values', () => {
  assert.equal(required(' value '), true);
  assert.equal(required('  '), false);
  assert.equal(serviceName('orders-api'), true);
  assert.equal(serviceName('Orders API'), false);
  assert.equal(versionName('v2-beta'), true);
  assert.equal(versionName('latest'), true);
  assert.equal(versionName('2026.08'), true);
  assert.equal(versionName('release candidate'), false);
  assert.equal(versionName('release\tcandidate'), false);
  assert.equal(versionName(''), false);
  assert.equal(permissionName('orders:v2:read'), true);
  assert.equal(permissionName('orders/read'), false);
  assert.equal(literalIP('127.0.0.1'), true);
  assert.equal(literalIP('service.internal'), false);
  assert.equal(portNumber(65535), true);
  assert.equal(portNumber(65536), false);
});

test('unprotected route regex validation accepts optional valid expressions', () => {
  assert.equal(routeRegex(''), true);
  assert.equal(routeRegex('^/(swagger|docs)(/.*)?$|\\.(js|ya?ml|jpe?g)$'), true);
  assert.equal(routeRegex('['), false);
  assert.equal(routeRegex('^/docs$\n^/other$'), false);
});

test('formatting escapes untrusted fields and creates stable grid records', () => {
  assert.equal(escapeHTML('<script>"x"</script>'), '&lt;script&gt;&quot;x&quot;&lt;/script&gt;');
  assert.equal(formatBytes(2048), '2.0 KiB');
  assert.equal(statusHTML('active<script>'), '<span class="status activescript">activescript</span>');
  assert.deepEqual(records([{ id: 'svc-1', name: 'orders' }]), [{ recid: 'svc-1', id: 'svc-1', name: 'orders' }]);
});

test('grid filtering supports all or selected columns', () => {
  const items = [{ name: 'orders', status: 'active' }, { name: 'billing', status: 'disabled' }];
  const columns = [{ field: 'name' }, { field: 'status' }];
  assert.deepEqual(filterGridRecords(items, columns, 'ACT'), [items[0]]);
  assert.deepEqual(filterGridRecords(items, columns, 'active', 'name'), []);
  assert.deepEqual(filterGridRecords(items, columns, '', 'name'), items);
});

test('grid pagination clamps pages and reports result bounds', () => {
  const items = Array.from({ length: 26 }, (_, index) => index + 1);
  assert.deepEqual(paginateGridRecords(items, 2, 25), { items: [26], page: 2, pageCount: 2, pageSize: 25, start: 26, end: 26, total: 26 });
  assert.equal(paginateGridRecords(items, 99, 10).page, 3);
  assert.deepEqual(paginateGridRecords([], 1, 25), { items: [], page: 1, pageCount: 1, pageSize: 25, start: 0, end: 0, total: 0 });
});

test('form list values normalize single and multiple W2UI selections', () => {
  assert.equal(selectedValue({ id: 'GET', text: 'GET' }), 'GET');
  assert.equal(selectedValue('POST'), 'POST');
  assert.deepEqual(selectedValues([{ id: 'GET' }, { id: 'POST' }, null]), ['GET', 'POST']);
  assert.deepEqual(choice(4100), { id: '4100', text: '4100' });
  assert.equal(optionalISOString('2030-01-02T03:04:05Z'), '2030-01-02T03:04:05.000Z');
  assert.equal(optionalISOString('not a date'), null);
  assert.equal(optionalISOString(''), '');
});

test('every management module provides actionable help content', () => {
  assert.deepEqual(Object.keys(MODULE_HELP).sort(), ['audit', 'configurations', 'consumers', 'gateways', 'overview', 'ports', 'services']);
  for (const content of Object.values(MODULE_HELP)) {
    assert.ok(content.introduction.length > 20);
    assert.ok(content.steps.length >= 3);
    assert.ok(content.tips.length >= 2);
  }
});

test('permission builder hides technical identifiers behind friendly labels', () => {
  assert.equal(buildPermissionName('orders/v1', 'invoke'), 'orders:v1:invoke');
  assert.equal(friendlyPermissionName('orders:v1:invoke'), 'Use orders (v1)');
  assert.deepEqual(permissionChoice({ name: 'reports:v2:read', description: 'Monthly reports' }), { id: 'reports:v2:read', text: 'View reports (v2) — Monthly reports' });
});

test('configuration workflow exposes only valid contextual actions', () => {
  assert.deepEqual(configurationActions({ status: 'draft' }), { validate: true, approve: false, activate: false, rollback: false });
  assert.equal(configurationActions({ status: 'validated', requiredApprovals: 2, approvalCount: 1 }).approve, true);
  assert.equal(configurationActions({ status: 'validated', requiredApprovals: 2, approvalCount: 2 }).activate, true);
  assert.equal(configurationActions({ status: 'superseded' }).rollback, true);
  assert.equal(approvalLabel({ requiredApprovals: 2, approvalCount: 1 }), '1 of 2');
  assert.match(configurationGuidance({ status: 'active' }), /live snapshot/);
});
