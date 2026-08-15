import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile, readdir, stat } from 'node:fs/promises';
import { dirname, join, normalize, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');

async function filesBelow(directory) {
  const result = [];
  for (const entry of await readdir(directory)) {
    const path = join(directory, entry);
    if ((await stat(path)).isDirectory()) result.push(...await filesBelow(path));
    else result.push(path);
  }
  return result;
}

test('every first-party relative module import resolves', async () => {
  const scripts = (await filesBelow(join(root, 'js'))).filter((path) => path.endsWith('.js'));
  for (const script of scripts) {
    const source = await readFile(script, 'utf8');
    const imports = [...source.matchAll(/(?:from\s+|import\s*\()(['"])(\.\.?\/[^'"]+)\1/g)];
    for (const match of imports) {
      const filePath = match[2].split(/[?#]/, 1)[0];
      const target = normalize(resolve(dirname(script), filePath));
      await assert.doesNotReject(stat(target), `${script} imports missing ${match[2]}`);
    }
  }
});

test('first-party dashboard does not persist credentials or use remote assets', async () => {
  const firstParty = (await filesBelow(root)).filter((path) => !path.includes(`${join(root, 'vendor')}/`) && !path.includes(`${join(root, 'tests')}/`));
  for (const path of firstParty) {
    const source = await readFile(path, 'utf8');
    assert.doesNotMatch(source, /\b(?:localStorage|sessionStorage)\b/, path);
    assert.doesNotMatch(source.replaceAll('http://www.w3.org/2000/svg', ''), /https?:\/\//, path);
  }
});

test('client connection guidance names the API key header', async () => {
  for (const relativePath of [
    'js/views/consumers.js',
    'js/components/secret-dialog.js',
    'js/components/listener-details-dialog.js',
    'js/components/help-content.js',
  ]) {
    const source = await readFile(join(root, relativePath), 'utf8');
    assert.match(source, /X-API-Key/, relativePath);
  }
});

test('configuration details use an explicit selection dialog', async () => {
  const view = await readFile(join(root, 'js/views/configurations.js'), 'utf8');
  const dialog = await readFile(join(root, 'js/components/configuration-details-dialog.js'), 'utf8');
  assert.match(view, /view-configuration-details/);
  assert.doesNotMatch(view, /configuration-inspector/);
  assert.match(dialog, /configuration-details-dialog/);
  assert.match(dialog, /configuration-dialog-actions/);
});

test('dashboard branding uses the local Shinmon logo', async () => {
  const index = await readFile(join(root, 'index.html'), 'utf8');
  const layout = await readFile(join(root, 'js/components/app-layout.js'), 'utf8');
  const logo = await readFile(join(root, 'assets/shinmon-logo.svg'), 'utf8');
  assert.match(index, /assets\/shinmon-logo\.svg/);
  assert.match(layout, /assets\/shinmon-logo\.svg/);
  assert.match(logo, /<title[^>]*>Shinmon logo<\/title>/);
});
