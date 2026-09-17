// Local reverse proxy that puts the Honco public website and Honco Chat
// (Mattermost) on ONE origin (http://localhost:8090), so Mattermost's
// HttpOnly MMAUTHTOKEN cookie is set and sent seamlessly.
//
//   /  /login  /signup  /forgot-password  /reset-password  /verify-email
//   /assets/*  /favicon.svg           -> the static website (dist/)
//   everything else (/, /api/*, /plugins/*, /static/*, /<team>/..., WS)
//                                     -> Mattermost on 127.0.0.1:8065
//
// Dependency-free (Node built-ins only). Caddy was not installed on this
// machine; Caddyfile in this folder is the equivalent config for when it is.
// Port 8090 is used because 8080 belongs to Jitsi JVB and must not be touched.

const http = require('http');
const net = require('net');
const fs = require('fs');
const path = require('path');

const PORT = Number(process.env.HONCO_PROXY_PORT || 8090);
const MM_HOST = '::1';
const MM_PORT = 8065;
const DIST = path.resolve(__dirname, 'dist');

// Clean website routes -> built HTML files.
const PAGES = {
    '/': 'index.html',
    '/login': 'login.html',
    '/signup': 'signup.html',
    '/forgot-password': 'forgot-password.html',
    '/reset-password': 'reset-password.html',
    '/verify-email': 'verify-email.html',
    '/favicon.svg': 'favicon.svg',
};

const MIME = {
    '.html': 'text/html;charset=utf-8', '.js': 'text/javascript', '.css': 'text/css',
    '.svg': 'image/svg+xml', '.png': 'image/png', '.jpg': 'image/jpeg', '.json': 'application/json',
    '.woff2': 'font/woff2', '.woff': 'font/woff', '.ico': 'image/x-icon', '.webp': 'image/webp', '.map': 'application/json',
};

function serveFile(res, file) {
    const full = path.resolve(DIST, '.' + (file.startsWith('/') ? file : '/' + file));
    if (!full.startsWith(DIST)) { res.writeHead(403); return res.end('forbidden'); }
    fs.readFile(full, (err, data) => {
        if (err) { res.writeHead(404, {'Content-Type': 'text/plain'}); return res.end('not found'); }
        res.writeHead(200, {'Content-Type': MIME[path.extname(full).toLowerCase()] || 'application/octet-stream'});
        res.end(data);
    });
}

const server = http.createServer((req, res) => {
    const url = req.url || '/';
    const p = url.split('?')[0];

    // Website (static) routes.
    if (Object.prototype.hasOwnProperty.call(PAGES, p)) {
        return serveFile(res, PAGES[p]);
    }
    if (p.startsWith('/assets/')) {
        return serveFile(res, p);
    }

    // Everything else -> Mattermost.
    const opts = {host: MM_HOST, port: MM_PORT, method: req.method, path: url, headers: req.headers};
    const upstream = http.request(opts, up => {
        res.writeHead(up.statusCode, up.headers);
        up.pipe(res);
    });
    upstream.on('error', () => { if (!res.headersSent) res.writeHead(502); res.end('upstream unavailable'); });
    req.pipe(upstream);
});

// WebSocket / Upgrade passthrough (Mattermost realtime at /api/v4/websocket).
server.on('upgrade', (req, socket, head) => {
    const upstream = net.connect(MM_PORT, MM_HOST, () => {
        let raw = req.method + ' ' + req.url + ' HTTP/1.1\r\n';
        for (let i = 0; i < req.rawHeaders.length; i += 2) {
            raw += req.rawHeaders[i] + ': ' + req.rawHeaders[i + 1] + '\r\n';
        }
        raw += '\r\n';
        upstream.write(raw);
        if (head && head.length) upstream.write(head);
        socket.pipe(upstream);
        upstream.pipe(socket);
    });
    upstream.on('error', () => socket.destroy());
    socket.on('error', () => upstream.destroy());
});

server.listen(PORT, '127.0.0.1', () => {
    console.log('Honco unified origin on http://localhost:' + PORT + '  (website + Mattermost ' + MM_HOST + ':' + MM_PORT + ')');
});
