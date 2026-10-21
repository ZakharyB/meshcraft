// MeshCraft rendezvous — v0.1 prototype
// Plain Node.js, built-ins only. Run: node signaling/rendezvous.js
//
// Job: peer discovery ONLY. Peers register under a shared networkId and
// look each other up. No game traffic, no world data, no keys held here.
// v1 adds: STUN endpoint reporting + hole-punch signal relay between
// two peers, and an encrypted relay fallback (DERP-style).

const http = require('http');
const PORT = Number(process.env.PORT || 4748);
const networks = new Map(); // networkId -> Map(nodeId -> {nodeName, agentHint, publicHint, at})

function json(res, code, obj) { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(obj, null, 2)); }
function readBody(req) { return new Promise((r) => { let b = ''; req.on('data', (c) => b += c); req.on('end', () => { try { r(JSON.parse(b || '{}')); } catch { r({}); } }); }); }

http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://x');
  if (url.pathname === '/register' && req.method === 'POST') {
    const { networkId, nodeId, nodeName, agentHint } = await readBody(req);
    if (!networkId || !nodeId) return json(res, 400, { error: 'networkId and nodeId required' });
    if (!networks.has(networkId)) networks.set(networkId, new Map());
    networks.get(networkId).set(nodeId, { nodeName, agentHint, publicHint: req.socket.remoteAddress, at: Date.now() });
    return json(res, 200, { ok: true, peers: [...networks.get(networkId).entries()].map(([id, v]) => ({ nodeId: id, ...v })) });
  }
  if (url.pathname === '/peers') {
    const net = networks.get(url.searchParams.get('networkId')) || new Map();
    // prune stale (>90s without re-register)
    for (const [id, v] of net) if (Date.now() - v.at > 90000) net.delete(id);
    return json(res, 200, { peers: [...net.entries()].map(([id, v]) => ({ nodeId: id, ...v })) });
  }
  json(res, 404, { error: 'not found' });
}).listen(PORT, () => console.log(`MeshCraft rendezvous on http://localhost:${PORT}`));
