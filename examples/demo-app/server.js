// A tiny scrum board, standing in for the system under test. It exists so the
// examples can be run without a private application: one HTTP server, no
// dependencies, state in memory.
//
//   node server.js [port]        # defaults to 8000
'use strict';

const http = require('node:http');
const { randomUUID } = require('node:crypto');

const port = Number(process.argv[2] ?? process.env.PORT ?? 8000);

const tokens = new Map(); // token → user
const cards = new Map(); // id → { title, column }

function send(res, status, body, type = 'application/json') {
  res.writeHead(status, { 'content-type': type });
  res.end(typeof body === 'string' ? body : JSON.stringify(body));
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    let data = '';
    req.on('data', (c) => (data += c));
    req.on('end', () => {
      try {
        resolve(data ? JSON.parse(data) : {});
      } catch (e) {
        reject(e);
      }
    });
  });
}

function userOf(req) {
  const auth = req.headers.authorization ?? '';
  return tokens.get(auth.replace(/^Bearer /, ''));
}

const LOGIN_PAGE = `<!doctype html>
<meta charset="utf-8"><title>Demo board — sign in</title>
<h1>Sign in</h1>
<form id="f">
  <label>User <input id="user" name="user" value=""></label>
  <button type="submit">Sign in</button>
</form>
<p id="status"></p>
<script>
  document.querySelector('#f').addEventListener('submit', async (e) => {
    e.preventDefault();
    const user = document.querySelector('#user').value;
    const r = await fetch('/api/login', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ user }),
    });
    const { token } = await r.json();
    localStorage.setItem('token', token);
    document.querySelector('#status').textContent = 'signed in';
  });
</script>`;

const BOARD_PAGE = `<!doctype html>
<meta charset="utf-8"><title>Demo board</title>
<h1>Board</h1>
<p id="whoami"></p>
<form id="new">
  <label>Title <input id="title" value="Card"></label>
  <button type="submit">New card</button>
</form>
<ul id="cards"></ul>
<script>
  const token = localStorage.getItem('token');
  const api = (path, body) => fetch(path, {
    method: 'POST',
    headers: { 'content-type': 'application/json', authorization: 'Bearer ' + token },
    body: JSON.stringify(body),
  }).then((r) => r.json());

  async function render() {
    const r = await fetch('/api/cards', { headers: { authorization: 'Bearer ' + token } });
    if (r.status === 401) { document.querySelector('#whoami').textContent = 'not signed in'; return; }
    const { user, cards } = await r.json();
    document.querySelector('#whoami').textContent = 'signed in as ' + user;
    document.querySelector('#cards').innerHTML = cards
      .map((c) => '<li data-testid="card" data-card-id="' + c.id + '" data-column="' + c.column + '">' +
        c.title + ' [' + c.column + '] <button data-move="' + c.id + '">Move to done</button></li>')
      .join('');
    for (const b of document.querySelectorAll('[data-move]')) {
      b.addEventListener('click', async () => {
        await api('/api/cards/' + b.dataset.move + '/move', { column: 'done' });
        render();
      });
    }
  }

  document.querySelector('#new').addEventListener('submit', async (e) => {
    e.preventDefault();
    await api('/api/cards', { title: document.querySelector('#title').value });
    render();
  });

  render();
</script>`;

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  const path = url.pathname;

  if (req.method === 'GET' && (path === '/' || path === '/login')) {
    return send(res, 200, LOGIN_PAGE, 'text/html; charset=utf-8');
  }
  if (req.method === 'GET' && path === '/board') {
    return send(res, 200, BOARD_PAGE, 'text/html; charset=utf-8');
  }

  if (req.method === 'POST' && path === '/api/login') {
    const { user } = await readBody(req);
    if (!user) return send(res, 400, { error: 'user is required' });
    const token = randomUUID();
    tokens.set(token, user);
    return send(res, 200, { token, user });
  }

  const user = userOf(req);
  if (!user) return send(res, 401, { error: 'not authenticated' });

  if (req.method === 'GET' && path === '/api/cards') {
    const list = [...cards].map(([id, c]) => ({ id, ...c }));
    return send(res, 200, { user, cards: list });
  }
  if (req.method === 'POST' && path === '/api/cards') {
    const { title } = await readBody(req);
    const id = String(cards.size + 1);
    cards.set(id, { title: title ?? 'Card', column: 'todo' });
    return send(res, 201, { id, title, column: 'todo' });
  }

  const move = path.match(/^\/api\/cards\/([^/]+)\/move$/);
  if (req.method === 'POST' && move) {
    const card = cards.get(move[1]);
    if (!card) return send(res, 404, { error: 'no such card' });
    const { column } = await readBody(req);
    card.column = column ?? 'done';
    return send(res, 200, { id: move[1], ...card });
  }

  send(res, 404, { error: 'not found' });
});

server.listen(port, () => console.log(`demo board on http://localhost:${port}`));
