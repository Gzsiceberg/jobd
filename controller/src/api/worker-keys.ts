import { z } from 'zod';

export const maxWorkerKeySeconds = 30 * 24 * 60 * 60;
const encoder = new TextEncoder();
const claimsSchema = z
  .object({
    version: z.literal(1),
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
      salt: encoder.encode('jobd.worker-key.v1'),
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

export async function issueWorkerKey(
  secret: string,
  queue: string,
  seconds: number,
  now = Math.floor(Date.now() / 1000),
) {
  if (
    !Number.isInteger(seconds) ||
    seconds < 1 ||
    seconds > maxWorkerKeySeconds
  )
    throw new Error('Invalid duration');
  const claims = claimsSchema.parse({
    version: 1,
    role: 'worker',
    queue,
    iat: now,
    exp: now + seconds,
    id: crypto.randomUUID(),
  });
  const payload =
    'jobd_worker_v1.' + encode(encoder.encode(JSON.stringify(claims)));
  const signature = await crypto.subtle.sign(
    'HMAC',
    await signingKey(secret),
    encoder.encode(payload),
  );
  return {
    api_key: payload + '.' + encode(new Uint8Array(signature)),
    expires_at: new Date(claims.exp * 1000).toISOString(),
    queue,
  };
}

export async function verifyWorkerKey(
  secret: string,
  token: string,
  now = Math.floor(Date.now() / 1000),
) {
  try {
    if (token.length > 2048) return null;
    const parts = token.split('.');
    if (parts.length !== 3 || parts[0] !== 'jobd_worker_v1') return null;
    if (
      !(await crypto.subtle.verify(
        'HMAC',
        await signingKey(secret),
        decode(parts[2]),
        encoder.encode(parts[0] + '.' + parts[1]),
      ))
    )
      return null;
    const claims = claimsSchema.parse(
      JSON.parse(new TextDecoder().decode(decode(parts[1]))),
    );
    if (
      claims.iat > now ||
      claims.exp <= now ||
      claims.exp <= claims.iat ||
      claims.exp - claims.iat > maxWorkerKeySeconds
    )
      return null;
    return claims;
  } catch {
    return null;
  }
}

/** Explicit allowlist: new administrative endpoints are denied by default. */
export function workerRouteAllowed(method: string, path: string): boolean {
  if (method === 'GET') return /^\/jobs(?:\/(?:latest|[0-9]+))?$/.test(path);
  if (method !== 'POST') return false;
  return (
    path === '/workers/register' ||
    /^\/workers\/[A-Za-z0-9_-]+\/(?:heartbeat|claim)$/.test(path) ||
    /^\/jobs\/[0-9]+\/(?:output|complete|fail)$/.test(path)
  );
}
