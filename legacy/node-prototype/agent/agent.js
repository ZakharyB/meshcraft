// MeshCraft agent — v0.1 prototype
// Plain Node.js, built-ins only. No npm install, no build step.
// Run: node agent/agent.js
//
// REAL in this prototype: status/capability reporting, lease-based
// single-writer election (local view), peer heartbeat + open-session
// announcements over plain HTTP, spawning a real server.jar.
// STUBBED for v1 (see ARCHITECTURE.md): WireGuard tunnel + hole
// punching (peers here talk agent-to-agent directly), Ed25519 signed
// config (owner-token placeholder below), world snapshot sync.

const http = require('http');
const fs = require('fs');
const os = require('os');
const path = require('path');
const crypto = require('crypto');
const { spawn } = require('child_process');

const ROOT = path.join(__dirname, '..');
const SERVER_DIR = path.join(ROOT, 'server');
const STATE_FILE = path.join(__dirname, 'state.json');

function loadConfig() {
  const file = path.join(__dirname, 'config.json');
  const base = JSON.parse(fs.readFileSync(path.join(__dirname, 'config.example.json'), 'utf8'));
  if (fs.existsSync(file)) Object.assign(base, JSON.parse(fs.readFileSync(file, 'utf8')));
  if (process.env.AGENT_PORT) base.agentPort = Number(process.env.AGENT_PORT);
  return base;
}
const cfg = loadConfig();

function loadState() {
  try { return JSON.parse(fs.readFileSync(STATE_FILE, 'utf8')); }
  catch { return { nodeId: crypto.randomUUID(), lease: null, sessionsSeen: [] }; }
}
const state = loadState();
const saveState = () => fs.writeFileSync(STATE_FILE, JSON.stringify(state, null, 2));

function capabilities() {
  const freeMb = Math.round(os.freemem() / 1048576);
  return {
    nodeId: state.nodeId, nodeName: cfg.nodeName,
    cpus: os.cpus().length, cpuModel: os.cpus()[0]?.model || '',
    freeRamMb: freeMb, totalRamMb: Math.round(os.totalmem() / 1048576),
    canHost: cfg.canHost && freeMb >= 1024,
    // crude score for election: prefer more free RAM + cores (v2: add ping)
    score: (cfg.canHost ? freeMb + os.cpus().length * 256 : 0),
  };
}

let mcProcess = null;
function startMinecraft() {
  const jar = path.join(SERVER_DIR, 'server.jar');
  if (!fs.existsSync(jar)) return { started: false, reason: 'no server.jar in server/ — lease granted, nothing spawned' };
  if (mcProcess) return { started: true, reason: 'already running' };
  mcProcess = spawn('java', [`-Xmx${cfg.maxRamMb}M`, '-jar', 'server.jar', 'nogui'], { cwd: SERVER_DIR });
  mcProcess.on('exit', () => { mcProcess = null; });
  return { started: true, reason: 'Paper jar spawned' };
}
function stopMinecraft() {
  if (!mcProcess) return false;
  mcProcess.stdin.write('stop\n'); return true;
}

// --- peer messaging (prototype transport: direct HTTP to peer agents) ---
const peersSeen = new Map(); // nodeId -> { ...cap, agentUrl, lastSeen }
function postJson(url, body) {
  return new Promise((resolve) => {
    const data = JSON.stringify(body);
    const req = http.request(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, timeout: 2500 },
      (res) => { res.resume(); res.on('end', resolve); });
    req.on('error', resolve); req.on('timeout', () => { req.destroy(); resolve(); });
    req.end(data);
  });
}
async function announce(endpoint, payload) {
  for (const peer of cfg.peers) await postJson(`${peer}${endpoint}`, payload);
}

// --- lease: exactly one game node at a time ---
function leaseValid() { return state.lease && Date.now() < state.lease.expiresAt; }
function takeLease(minutes = 60) {
  state.lease = { holder: state.nodeId, holderName: cfg.nodeName,
    sessionId: crypto.randomUUID(), expiresAt: Date.now() + minutes * 60000 };
  saveState(); return state.lease;
}

// Owner control — PROTOTYPE ONLY: a shared token file. v0.3 replaces
// this with Ed25519-signed config bundles (see ARCHITECTURE.md §6).
function isOwner(req) {
  const tokenFile = path.join(__dirname, 'owner-token.txt');
  if (!fs.existsSync(tokenFile)) return false;
  return req.headers['x-owner-token'] === fs.readFileSync(tokenFile, 'utf8').trim();
}

function json(res, code, obj) {
  res.writeHead(code, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(obj, null, 2));
}
function readBody(req) {
  return new Promise((r) => { let b = ''; req.on('data', (c) => b += c); req.on('end', () => { try { r(JSON.parse(b || '{}')); } catch { r({}); } }); });
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://x');

  if (url.pathname === '/api/status') return json(res, 200, {
    ...capabilities(), agentPort: cfg.agentPort, mcPort: cfg.mcPort,
    mcRunning: !!mcProcess, lease: leaseValid() ? state.lease : null,
    peers: [...peersSeen.values()],
  });

  if (url.pathname === '/api/heartbeat' && req.method === 'POST') {
    const body = await readBody(req);
    if (body.nodeId && body.nodeId !== state.nodeId)
      peersSeen.set(body.nodeId, { ...body, lastSeen: new Date().toISOString() });
    return json(res, 200, { ok: true });
  }

  // A peer asks the mesh to open the server.
  if (url.pathname === '/api/request-open' && req.method === 'POST') {
    const body = await readBody(req);
    if (leaseValid() && state.lease.holder !== state.nodeId) {
      // Someone else already hosts — joining beats forking the world.
      return json(res, 200, { action: 'join', lease: state.lease });
    }
    // Prototype election = self if capable (v2: compare peer scores).
    if (!capabilities().canHost) return json(res, 200, { action: 'cannot-host', reason: 'this node advertises canHost:false or <1GB free RAM — in the full mesh another peer would be elected here' });
    const lease = takeLease();
    const mc = startMinecraft();
    await announce('/api/announce-open', { lease, openedBy: body.requester || cfg.nodeName, mc });
    return json(res, 200, { action: 'host', lease, mc });
  }

  if (url.pathname === '/api/announce-open' && req.method === 'POST') {
    const body = await readBody(req);
    state.sessionsSeen.push({ ...body, at: new Date().toISOString() });
    if (body.lease) { state.lease = body.lease; saveState(); }
    console.log(`[mesh] server opened by ${body.openedBy} on ${body.lease?.holderName} (session ${body.lease?.sessionId})`);
    return json(res, 200, { ok: true });
  }

  if (url.pathname === '/api/stop' && req.method === 'POST') {
    if (!isOwner(req)) return json(res, 403, { error: 'owner token required (prototype auth — Ed25519 in v0.3)' });
    const stopped = stopMinecraft();
    state.lease = null; saveState();
    await announce('/api/announce-stop', { by: cfg.nodeName });
    return json(res, 200, { stopped });
  }
  if (url.pathname === '/api/announce-stop' && req.method === 'POST') {
    state.lease = null; saveState(); return json(res, 200, { ok: true });
  }

  if (url.pathname === '/') {
    res.writeHead(200, { 'Content-Type': 'text/html' });
    return res.end(DASHBOARD);
  }
  json(res, 404, { error: 'not found' });
});

const DASHBOARD = `<!doctype html><meta charset=utf-8><title>MeshCraft</title>
<style>body{background:#111;color:#eee;font-family:system-ui;max-width:640px;margin:40px auto;padding:0 16px}
button{background:#2ea043;color:#fff;border:0;padding:10px 18px;border-radius:8px;cursor:pointer;font-size:15px}
pre{background:#1c1c1c;padding:12px;border-radius:8px;overflow:auto}</style>
<h1>⛏️ MeshCraft — <span id=n></span></h1>
<button onclick="open_()">Request Open (play)</button>
<pre id=s>loading…</pre>
<script>
async function load(){const d=await(await fetch('/api/status')).json();n.textContent=d.nodeName;s.textContent=JSON.stringify(d,null,2)}
async function open_(){const r=await(await fetch('/api/request-open',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({requester:d_name()})})).json();alert(JSON.stringify(r,null,2));load()}
function d_name(){return n.textContent} load(); setInterval(load,5000);
</script>`;

server.listen(cfg.agentPort, async () => {
  console.log(`MeshCraft agent "${cfg.nodeName}" (${state.nodeId}) on http://localhost:${cfg.agentPort}`);
  // Heartbeat to configured peers every 20s.
  const beat = () => announce('/api/heartbeat', { ...capabilities(), agentUrl: `http://localhost:${cfg.agentPort}` });
  beat(); setInterval(beat, 20000);
});
