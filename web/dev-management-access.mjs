import { isIP } from 'node:net';

// 开发代理会把远端连接变成本机连接，必须在代理前保留相同的管理边界。
export function localManagementProxyGuard(req, res, next) {
  if (!req.url?.startsWith('/api')) return next();
  const peer = req.socket?.remoteAddress?.toLowerCase() ?? '';
  const local = peer === '::1'
    || (isIP(peer) === 4 && peer.startsWith('127.'))
    || (peer.startsWith('::ffff:') && isIP(peer.slice(7)) === 4 && peer.slice(7).startsWith('127.'));
  if (!local) {
    res.statusCode = 403;
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ error: '管理接口仅允许本机访问' }));
    return;
  }
  next();
}
