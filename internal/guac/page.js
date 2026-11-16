// Runs inside the Guacamole tab. Go calls it as (this function)(op) and gets
// back a plain value. It reaches the page's Guacamole.Client through the
// AngularJS service guacClientManager, which Guacamole 1.4 to 1.6 all have.
//
// Chrome runs timers in a background tab at most once a second, so waits
// that must be short happen in Go between calls. The page side only waits
// for events from guacd.
async function guac(op) {
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

  // The text of Guacamole's status dialog ("Disconnected: You have been
  // disconnected."), or '' when no dialog shows.
  function dialog() {
    const n = document.querySelector('.client-status-modal .notification');
    if (!n || n.offsetParent === null) return '';
    const part = (sel) => (n.querySelector(sel)?.innerText || '').trim();
    return [part('.title'), part('.body .text')].filter(Boolean).join(': ');
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
  // our last click and the function that gives the clipboard back.
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
    // fills them with a translucent color. Catch the first such fill.
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
      if (a > 0 && a < 255 && path.length) drawn.poke({ layer, rects: path });
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

  if (op === 'idle') return Date.now() - st.lastClick;
  if (op === 'restore') return giveBackClipboard() || {};

  const mc = managedClient();
  if (op === 'session') {
    if (!mc) return null;
    return { state: String(mc.clientState.connectionState).toLowerCase(), name: mc.name || mc.title || '' };
  }
  if (!mc) return { error: 'this tab shows no Guacamole session' };
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
