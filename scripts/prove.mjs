#!/usr/bin/env node
// End-to-end check over raw CDP (Node 22+, no dependencies):
// log in to Guacamole, open the SSH connection, type a command, take a screenshot.
//
//   node scripts/prove.mjs [screenshot.png]
//
// Env (defaults match .env): CDP_URL, GUAC_URL, GUAC_USER, GUAC_PASSWORD,
// CONNECTION, COMMAND.
import { writeFileSync } from 'node:fs';

const CDP_URL = process.env.CDP_URL ?? 'http://127.0.0.1:47922';
const GUAC_URL = process.env.GUAC_URL ?? 'http://127.0.0.1:47880/guacamole/';
const USER = process.env.GUAC_USER ?? 'guacuser';
const PASSWORD = process.env.GUAC_PASSWORD ?? 'guacpass';
const CONNECTION = process.env.CONNECTION ?? 'ssh-target';
const COMMAND = process.env.COMMAND ??
  'echo GUAC-OK-$((6*7)) | tee /tmp/guac-proof.txt; uname -m; hostname; whoami';
const OUT = process.argv[2] ?? 'guac-terminal.png';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a);

// Open a fresh tab so the run does not disturb other tabs.
const target = await (await fetch(`${CDP_URL}/json/new?about:blank`, { method: 'PUT' })).json();
const ws = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
let nextId = 1;
const pending = new Map();
ws.onmessage = (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && pending.has(msg.id)) {
    const { resolve, reject } = pending.get(msg.id);
    pending.delete(msg.id);
    msg.error ? reject(new Error(JSON.stringify(msg.error))) : resolve(msg.result);
  }
};
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const id = nextId++;
  pending.set(id, { resolve, reject });
  ws.send(JSON.stringify({ id, method, params }));
});
const evaluate = async (expression) =>
  (await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })).result.value;
const waitFor = async (what, expression, timeoutMs = 20000) => {
  const end = Date.now() + timeoutMs;
  while (Date.now() < end) {
    const v = await evaluate(expression).catch(() => undefined);
    if (v) return v;
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${what}`);
};

// Keys the Guacamole keyboard handler needs as key events rather than text.
const SPECIAL = { '\n': { key: 'Enter', code: 'Enter', keyCode: 13, text: '\r' } };
async function typeText(text) {
  for (const ch of text) {
    const k = SPECIAL[ch] ?? { key: ch, text: ch, keyCode: ch.toUpperCase().charCodeAt(0) };
    await send('Input.dispatchKeyEvent', {
      type: 'keyDown', key: k.key, code: k.code, text: k.text, unmodifiedText: k.text,
      windowsVirtualKeyCode: k.keyCode,
    });
    await send('Input.dispatchKeyEvent', {
      type: 'keyUp', key: k.key, code: k.code, windowsVirtualKeyCode: k.keyCode,
    });
  }
}

try {
  await send('Page.enable');
  await send('Runtime.enable');

  // Guacamole keeps its auth token in localStorage. Clear it so the run
  // always goes through the login form.
  await send('Storage.clearDataForOrigin', {
    origin: new URL(GUAC_URL).origin, storageTypes: 'local_storage,session_storage,cookies',
  });
  log('open', GUAC_URL);
  await send('Page.navigate', { url: GUAC_URL });
  await waitFor('login form', `!!document.querySelector('.login-ui input[type=password]')`);

  log('log in as', USER);
  await evaluate(`document.querySelector('.login-ui input[type=text]').focus()`);
  await typeText(USER);
  await evaluate(`document.querySelector('.login-ui input[type=password]').focus()`);
  await typeText(PASSWORD + '\n');
  await waitFor('login to finish', `!document.querySelector('.login-ui input[type=password]') ||
      document.querySelector('.login-ui')?.offsetParent === null`);

  // A user with exactly one connection lands on it right after login.
  // Otherwise open it by URL: #/client/<base64(identifier NUL "c" NUL dataSource)>,
  // where the identifier is the connection name for user-mapping.xml.
  const landed = await waitFor('home or client page',
    `location.hash.startsWith('#/client/') ? 'client' : (document.querySelector('.home, .connection-list, guac-group-list') ? 'home' : '')`);
  if (landed === 'client') {
    log('auto-connected after login', await evaluate('location.href'));
  } else {
    const clientId = Buffer.from(`${CONNECTION}\0c\0default`).toString('base64').replace(/=+$/, '');
    const clientUrl = new URL(`#/client/${clientId}`, GUAC_URL).href;
    log('open connection', CONNECTION, clientUrl);
    await send('Page.navigate', { url: clientUrl });
  }
  // Connected = a large display canvas exists and the status dialog
  // ("Connecting...", errors) is not visible.
  await waitFor('terminal canvas', `(() => {
      const big = [...document.querySelectorAll('canvas')].some(c => c.width > 200 && c.height > 200);
      const status = document.querySelector('.client-status-modal');
      const statusVisible = status && status.offsetParent !== null && status.innerText.trim() !== '';
      return big && !statusVisible;
    })()`, 30000);
  await sleep(2000); // let the shell prompt arrive

  // Count trusted keydowns as a check that input reaches the page. It stays 0
  // when Chromium's password manager prompt blocks the tab (see chromium.sh).
  await evaluate(`window.__keydowns = 0; window.addEventListener('keydown', () => window.__keydowns++, true); 0`);
  log('type command:', COMMAND);
  await typeText(COMMAND + '\n');
  log('keydown events seen by the page:', await evaluate('window.__keydowns'));
  await sleep(2000);

  const shot = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(OUT, Buffer.from(shot.data, 'base64'));
  const info = await evaluate(`({
      title: document.title,
      url: location.href,
      canvasSizes: [...document.querySelectorAll('canvas')].map(c => c.width + 'x' + c.height),
    })`);
  log('screenshot saved to', OUT, JSON.stringify(info));
} finally {
  ws.close();
  if (!process.env.KEEP_TAB) await fetch(`${CDP_URL}/json/close/${target.id}`).catch(() => {});
}
