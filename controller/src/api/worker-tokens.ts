import { z } from 'zod';

export const maxWorkerTokenSeconds = 30 * 24 * 60 * 60;
const encoder = new TextEncoder();
const prefix = 'jw2.';
// Binary layout: uint32 issued-at, uint32 expiry (big endian), 16-byte UUID,
// ASCII queue (1–63 bytes), then the full 32-byte HMAC-SHA-256 signature.
const headerBytes = 24;
const signatureBytes = 32;
const claimsSchema = z
  .object({
    version: z.literal(2),
    role: z.literal('worker'),
    queue: z.string().regex(/^[a-z0-9][a-z0-9_-]{0,62}$/),
    iat: z.number().int().nonnegative(),
    exp: z.number().int().nonnegative(),
    id: z.string().uuid(),
  })
  .strict();

function encode(bytes: Uint8Array): string {
  return btoa(String.fromCharCode(...bytes))
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replace(/=+$/, '');
}

function decode(value: string): Uint8Array {
  if (!/^[A-Za-z0-9_-]+$/.test(value)) throw new Error('Invalid encoding');
  const bytes = Uint8Array.from(
    atob(value.replaceAll('-', '+').replaceAll('_', '/')),
    (c) => c.charCodeAt(0),
  );
  if (encode(bytes) !== value) throw new Error('Non-canonical encoding');
  return bytes;
}

async function signingKey(secret: string): Promise<CryptoKey> {
  const material = await crypto.subtle.importKey(
    'raw',
    encoder.encode(secret),
    'HKDF',
    false,
    ['deriveKey'],
  );
  return crypto.subtle.deriveKey(
    {
      name: 'HKDF',
      hash: 'SHA-256',
      // Keep this protocol constant unchanged: renaming it invalidates existing tokens.
      salt: encoder.encode('jobd.worker-key.v2'),
      info: encoder.encode('worker-token-signing'),
    },
    material,
    { name: 'HMAC', hash: 'SHA-256', length: 256 },
    false,
    ['sign', 'verify'],
  );
}

export async function isAdminKey(
  candidate: string,
  secret: string,
): Promise<boolean> {
  const hashes = await Promise.all(
    [candidate, secret].map((value) =>
      crypto.subtle.digest('SHA-256', encoder.encode(value)),
    ),
  );
  const a = new Uint8Array(hashes[0]);
  const b = new Uint8Array(hashes[1]);
  let difference = 0;
  for (let i = 0; i < a.length; i++) difference |= a[i] ^ b[i];
  return difference === 0;
}

export async function issueWorkerToken(
  secret: string,
  queue: string,
  seconds: number,
  now = Math.floor(Date.now() / 1000),
) {
  if (
    !Number.isInteger(seconds) ||
    seconds < 1 ||
    seconds > maxWorkerTokenSeconds
  )
    throw new Error('Invalid duration');
  const claims = claimsSchema.parse({
    version: 2,
    role: 'worker',
    queue,
    iat: now,
    exp: now + seconds,
    id: crypto.randomUUID(),
  });
  if (claims.iat > 0xffffffff || claims.exp > 0xffffffff)
    throw new Error('Invalid timestamp');
  const payload = new Uint8Array(headerBytes + queue.length);
  const view = new DataView(payload.buffer);
  view.setUint32(0, claims.iat);
  view.setUint32(4, claims.exp);
  const id = claims.id.replaceAll('-', '');
  for (let i = 0; i < 16; i++)
    payload[8 + i] = parseInt(id.slice(i * 2, i * 2 + 2), 16);
  payload.set(encoder.encode(queue), headerBytes);
  const signature = await crypto.subtle.sign(
    'HMAC',
    await signingKey(secret),
    payload,
  );
  const token = new Uint8Array(payload.length + signatureBytes);
  token.set(payload);
  token.set(new Uint8Array(signature), payload.length);
  return {
    token: prefix + encode(token),
    expires_at: new Date(claims.exp * 1000).toISOString(),
    queue,
  };
}

export async function verifyWorkerToken(
  secret: string,
  token: string,
  now = Math.floor(Date.now() / 1000),
) {
  try {
    if (!token.startsWith(prefix) || token.length > 163) return null;
    const bytes = decode(token.slice(prefix.length));
    if (
      bytes.length < headerBytes + 1 + signatureBytes ||
      bytes.length > headerBytes + 63 + signatureBytes
    )
      return null;
    const payload = bytes.slice(0, -signatureBytes);
    if (
      !(await crypto.subtle.verify(
        'HMAC',
        await signingKey(secret),
        bytes.slice(-signatureBytes),
        payload,
      ))
    )
      return null;
    const view = new DataView(payload.buffer);
    const id = Array.from(payload.slice(8, headerBytes), (byte) =>
      byte.toString(16).padStart(2, '0'),
    ).join('');
    const claims = claimsSchema.parse({
      version: 2,
      role: 'worker',
      queue: new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(
        payload.slice(headerBytes),
      ),
      iat: view.getUint32(0),
      exp: view.getUint32(4),
      id: `${id.slice(0, 8)}-${id.slice(8, 12)}-${id.slice(12, 16)}-${id.slice(16, 20)}-${id.slice(20)}`,
    });
    if (
      claims.iat > now ||
      claims.exp <= now ||
      claims.exp <= claims.iat ||
      claims.exp - claims.iat > maxWorkerTokenSeconds
    )
      return null;
    return claims;
  } catch {
    return null;
  }
}

/** Explicit allowlist: new administrative endpoints are denied by default. */
export function workerRouteAllowed(method: string, path: string): boolean {
  if (method === 'GET')
    return (
      path === '/auth/worker-token/verify' ||
      /^\/jobs(?:\/(?:latest|[0-9]+))?$/.test(path)
    );
  if (method !== 'POST') return false;
  return (
    path === '/workers/register' ||
    /^\/workers\/[A-Za-z0-9_-]+\/(?:heartbeat|claim)$/.test(path) ||
    /^\/jobs\/[0-9]+\/(?:output|complete|fail)$/.test(path)
  );
}
