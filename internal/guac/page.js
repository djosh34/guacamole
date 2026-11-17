// Runs inside the Guacamole tab. Go calls it as (this function)(op, arg) and
// gets back a plain value. It reaches the page's Guacamole.Client through the
// AngularJS service guacClientManager, which Guacamole 1.4 to 1.6 all have.
//
// Chrome runs timers in a background tab at most once a second, so waits
// that must be short happen in Go between calls. The page side only waits
// for events from guacd.
async function guac(op, arg) {
  // The client shown by this tab, or null when the tab shows none.
  function managedClient() {
    const ng = window.angular;
    const injector = ng && ng.element(document.documentElement).injector();
    if (!injector || !injector.has('guacClientManager')) return null;
    const m = /^#\/client\/([^/?]+)/.exec(location.hash);
    if (!m) return null;
    const clients = injector.get('guacClientManager').getManagedClients();
    // A tab with several tiled connections has their ids joined with '.'.
    for (const id of decodeURIComponent(m[1]).split('.')) {
      if (clients[id]) return clients[id];
    }
    return null;
  }

  // The text of Guacamole's status dialog ("You have been disconnected."),
  // or its title when it has no text, or '' when no dialog shows. When the
  // dialog has a Reconnect button, the text says how to use it.
  function dialog() {
    const n = document.querySelector('.client-status-modal .notification');
    if (!n || n.offsetParent === null) return '';
    const part = (sel) => (n.querySelector(sel)?.innerText || '').trim();
    const text = part('.body .text') || part('.title');
    return reconnectButton() ? text + ' Run guac reconnect.' : text;
  }

  // Guacamole's Reconnect button, or null when none shows.
  function reconnectButton() {
    const b = document.querySelector('.client-status-modal button.reconnect');
    return b && b.offsetParent !== null ? b : null;
  }

  // Resolves with the first truthy value that check returns when poke is
  // called, or with null after ms.
  function waitEvent(ms) {
    let done;
    const p = new Promise((resolve) => {
      done = resolve;
      setTimeout(() => resolve(null), ms);
    });
    return { promise: p, poke: (v) => v && done(v) };
  }

  // Our state between calls: the mouse position of the drag end, the time of
  // our last click, the function that gives the clipboard back and the
  // frames drawn since the last keys.
  const st = (window.__guac = window.__guac || { lastClick: 0 });

  function giveBackClipboard() {
    if (st.restore) st.restore();
    st.restore = null;
  }

  // Selects the whole visible terminal with protocol mouse events and returns
  // the text guacd copies for it. Our clipboard handler stays until 'restore',
  // so the copy and any stray copy after it never reach the user's clipboard.
  async function copy(mc) {
    giveBackClipboard();
    const client = mc.client;
    const display = client.getDisplay();
    const mouse = (x, y, buttons) => mc.tunnel.sendMessage('mouse', x, y, buttons);

    const copied = waitEvent(3000);
    const onclipboard = client.onclipboard;
    client.onclipboard = (stream) => {
      // Guacamole.StringReader breaks characters outside the BMP, such as
      // emoji, so decode the UTF-8 bytes here.
      const reader = new Guacamole.ArrayBufferReader(stream);
      const utf8 = new TextDecoder();
      let text = '';
      reader.ondata = (data) => { text += utf8.decode(data, { stream: true }); };
      reader.onend = () => copied.poke({ text: text + utf8.decode() });
    };
    const restore = () => { client.onclipboard = onclipboard; };
    st.restore = restore;
    // If Go never calls 'restore', give the clipboard back anyway.
    setTimeout(() => st.restore === restore && giveBackClipboard(), 5000);

    // guacd draws the selection highlight as rects on its selection layer and
    // fills them with a translucent color. Catch the first such fill that is
    // as wide as the drag below. The scrollbar handle is translucent too, and
    // guacd redraws it while output comes in.
    const drawn = waitEvent(3000);
    const paths = new Map();
    const rect = display.rect;
    const fillColor = display.fillColor;
    display.rect = function (layer, x, y, w, h) {
      if (!paths.has(layer)) paths.set(layer, []);
      paths.get(layer).push({ x, y, w, h });
      return rect.apply(this, arguments);
    };
    display.fillColor = function (layer, r, g, b, a) {
      const path = paths.get(layer) || [];
      paths.delete(layer);
      if (a > 0 && a < 255 && path.length && path[0].w >= display.getWidth() / 4) drawn.poke({ layer, rects: path });
      return fillColor.apply(this, arguments);
    };

    // Press at the top left and drag along the first row. The highlight of
    // that one row gives the row height and the selection layer.
    mouse(0, 0, 1);
    mouse(Math.floor(display.getWidth() / 2), 0, 1);
    const probe = await drawn.promise;
    display.rect = rect;
    display.fillColor = fillColor;
    if (!probe) {
      mouse(0, 0, 0);
      st.lastClick = Date.now();
      return { error: dialog() || 'the terminal drew no selection' };
    }
    const rowHeight = probe.rects[0].h;
    let left = 0;
    let top = 0;
    for (let l = probe.layer; l; l = l.parent) {
      left += l.x;
      top += l.y;
    }
    // The selection layer covers the text grid exactly. End the drag in the
    // middle of the last row and on the last column: a drag below the last
    // row copies rows that are not on the screen.
    const rows = Math.floor(probe.layer.height / rowHeight);
    st.x = left + probe.layer.width - 1;
    st.y = top + (rows - 1) * rowHeight + Math.floor(rowHeight / 2);
    mouse(st.x, st.y, 1);
    mouse(st.x, st.y, 0);
    st.lastClick = Date.now();

    const got = await copied.promise;
    if (!got) return { error: dialog() || 'guacd sent no text for the selection; copying may be disabled on this connection' };
    return got;
  }

  // Resolves with '' once guacd has handled every instruction sent before,
  // or with an error. guacd answers a pipe stream it does not know with an
  // error ack right away, in order with the other instructions, and sends
  // nothing for keys or a clipboard stream that work.
  function sync(mc) {
    const answered = waitEvent(5000);
    const stream = mc.client.createPipeStream('text/plain', 'guac-sync');
    stream.onack = () => {
      stream.sendEnd();
      answered.poke('answered');
    };
    return answered.promise.then((v) => (v ? '' : dialog() || 'guacd did not answer'));
  }

  // Counts the frames guacd draws from now on, for 'frames'.
  function countFrames(client) {
    st.frames = 0;
    if (client.__guacCounts) return;
    client.__guacCounts = true;
    const onsync = client.onsync;
    client.onsync = function () {
      st.frames++;
      return onsync && onsync.apply(this, arguments);
    };
  }

  // Sends key events: [pressed, keysym] pairs.
  async function keys(mc, events) {
    countFrames(mc.client);
    st.lastKeys = Date.now();
    for (const [pressed, keysym] of events) mc.client.sendKeyEvent(pressed ? 1 : 0, keysym);
    const err = await sync(mc);
    return err ? { error: err } : {};
  }

  // Puts text on guacd's clipboard as UTF-8. Guacamole.StringWriter would
  // break characters outside the BMP, such as emoji. The stream ends only
  // after sync: ending it makes Guacamole.Client drop the stream, and with it
  // an error ack that is still on its way.
  async function clipboard(mc, text) {
    const stream = mc.client.createClipboardStream('text/plain');
    const writer = new Guacamole.ArrayBufferWriter(stream);
    let refused = '';
    writer.onack = (status) => {
      if (status.isError()) refused = refused || status.message || 'error ' + status.code;
    };
    writer.sendData(new TextEncoder().encode(text));
    const err = await sync(mc);
    if (err) return { error: err };
    if (refused) return { error: 'guacd refused the pasted text: ' + refused };
    writer.sendEnd();
    return {};
  }

  // Clicks Reconnect in Guacamole's disconnect dialog. Without the button,
  // wait is true while it may still come: Guacamole shows it about a second
  // after the session ends, and until then the session reads as connected.
  // So wait while the session ends, or after guac sent keys, such as exit, a
  // moment ago.
  function reconnect(mc) {
    const button = reconnectButton();
    if (!button) {
      const state = mc ? String(mc.clientState.connectionState).toLowerCase() : '';
      const wait = state !== 'connected' || Date.now() - (st.lastKeys || 0) < 3000;
      return { error: dialog() || 'no Reconnect button showed' + (state && '; the session is ' + state), wait };
    }
    button.click();
    st.lastKeys = 0;
    return {};
  }

  if (op === 'idle') return Date.now() - st.lastClick;
  if (op === 'frames') return st.frames || 0;
  if (op === 'restore') return giveBackClipboard() || {};

  const mc = managedClient();
  if (op === 'session') {
    if (!mc) return null;
    return { state: String(mc.clientState.connectionState).toLowerCase(), name: mc.name || mc.title || '', dialog: dialog() };
  }
  if (op === 'reconnect') return reconnect(mc);
  if (!mc) return { error: 'this tab shows no Guacamole session' };
  if (op === 'keys') return keys(mc, arg);
  if (op === 'clipboard') return clipboard(mc, arg);
  if (op === 'copy') return copy(mc);
  if (op === 'clear') {
    // One click clears the highlight.
    mc.tunnel.sendMessage('mouse', st.x, st.y, 1);
    mc.tunnel.sendMessage('mouse', st.x, st.y, 0);
    st.lastClick = Date.now();
    return {};
  }
  return { error: 'unknown operation ' + op };
}
