// app://gleam/ -> http://127.0.0.1:<port>/ proxy.
//
// Why a custom scheme instead of loading the loopback URL directly:
//  - a stable origin (app://gleam) keeps localStorage prefs across launches even though the port changes;
//  - the token lives only in the main process and is attached here, per request;
//  - the renderer never talks to the loopback server directly.
import { createHash } from 'node:crypto';
import { net, protocol } from 'electron';

export const APP_SCHEME = 'app';
export const APP_HOST = 'gleam';
export const APP_ORIGIN = `${APP_SCHEME}://${APP_HOST}`;
const TOKEN_HEADER = 'X-Gleam-Token';

/** Must run before app 'ready'. */
export function registerAppScheme(): void {
  protocol.registerSchemesAsPrivileged([
    {
      scheme: APP_SCHEME,
      privileges: { standard: true, secure: true, supportFetchAPI: true, stream: true, codeCache: true },
    },
  ]);
}

export interface Upstream {
  addr: string;
  token: string;
}

// Headers the renderer sends that must not reach the Go guard as-is:
//  - Origin / Sec-Fetch-*: describe app://gleam, which the loopback guard (correctly) treats as foreign.
//    The origin check is enforced here instead (only app://gleam may call through).
//  - Cookie / Host / Referer: meaningless for the upstream; Host is set by net.fetch.
const DROP_HEADERS = new Set(['origin', 'referer', 'cookie', 'host', TOKEN_HEADER.toLowerCase()]);

export function installAppProtocol(getUpstream: () => Upstream | null): void {
  protocol.handle(APP_SCHEME, async (req) => {
    const url = new URL(req.url);
    if (url.host !== APP_HOST) return new Response('not found', { status: 404 });
    const origin = req.headers.get('origin');
    if (origin && origin !== APP_ORIGIN) return new Response('forbidden origin', { status: 403 });
    const up = getUpstream();
    if (!up) return new Response('backend not running', { status: 503 });

    const headers = new Headers();
    req.headers.forEach((value, key) => {
      const k = key.toLowerCase();
      if (DROP_HEADERS.has(k) || k.startsWith('sec-fetch-')) return;
      headers.set(key, value);
    });
    headers.set(TOKEN_HEADER, up.token);

    const upstream = new AbortController();
    const init: RequestInit & { bypassCustomProtocolHandlers?: boolean; duplex?: 'half' } = {
      method: req.method,
      headers,
      signal: upstream.signal,
      bypassCustomProtocolHandlers: true,
    };
    if (req.body && req.method !== 'GET' && req.method !== 'HEAD') {
      init.body = req.body;
      init.duplex = 'half';
    }
    const res = await net.fetch(`http://${up.addr}${url.pathname}${url.search}`, init);

    const type = res.headers.get('content-type') || '';
    if (!type.startsWith('text/html')) return cancelable(res, upstream);
    // HTML documents get a CSP. Spike shortcut: the inline-script hashes are computed from the
    // document actually served (it comes from the sidecar's embedded assets); Phase 1 computes them at build time.
    const html = await res.text();
    const out = new Headers(res.headers);
    out.set('Content-Security-Policy', buildCSP(html));
    out.delete('content-length');
    return new Response(html, { status: res.status, statusText: res.statusText, headers: out });
  });
}

/**
 * Returning the upstream Response as-is means that when the renderer drops a request (EventSource.close(),
 * page reload) the upstream net.fetch keeps running: long-lived SSE streams then pile up in Chromium's
 * 6-connections-per-host pool until every app:// request stalls (measured in the spike). Re-wrapping the
 * body lets the renderer-side cancel reach us, and we abort the upstream request with it.
 */
function cancelable(res: Response, upstream: AbortController): Response {
  if (!res.body) return res;
  const reader = res.body.getReader();
  const body = new ReadableStream<Uint8Array>({
    async pull(ctrl) {
      try {
        const { done, value } = await reader.read();
        if (done) ctrl.close();
        else ctrl.enqueue(value);
      } catch (err) {
        ctrl.error(err);
      }
    },
    cancel(reason) {
      upstream.abort(reason);
      return reader.cancel(reason).catch(() => {});
    },
  });
  return new Response(body, { status: res.status, statusText: res.statusText, headers: res.headers });
}

function inlineScriptHashes(html: string): string[] {
  const hashes: string[] = [];
  const re = /<script(?![^>]*\bsrc=)[^>]*>([\s\S]*?)<\/script>/gi;
  for (let m = re.exec(html); m; m = re.exec(html)) {
    hashes.push(`'sha256-${createHash('sha256').update(m[1], 'utf8').digest('base64')}'`);
  }
  return hashes;
}

function buildCSP(html: string): string {
  return [
    "default-src 'self'",
    `script-src 'self' ${inlineScriptHashes(html).join(' ')}`.trim(),
    // The UI sets inline style attributes; Google Fonts stays for the spike (decision D5).
    "style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
    "font-src 'self' https://fonts.gstatic.com data:",
    "img-src 'self' data: blob: https:",
    "connect-src 'self'",
    // Browser preview pane embeds a user-entered http(s) page (typically a local dev server).
    'frame-src http: https:',
    "object-src 'none'",
    "base-uri 'none'",
    "form-action 'none'",
    "frame-ancestors 'none'",
  ].join('; ');
}
