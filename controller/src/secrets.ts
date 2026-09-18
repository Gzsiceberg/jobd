import { ApiError } from './api/errors';

const encoder = new TextEncoder();
const maxVariables = 64;
export const validEnvName = (name: string) =>
  /^[A-Za-z_][A-Za-z0-9_]{0,127}$/.test(name);

function encode(data: Uint8Array): string {
  return btoa(String.fromCharCode(...data));
}
function decode(data: string): Uint8Array<ArrayBuffer> {
  return Uint8Array.from(atob(data), (character) => character.charCodeAt(0));
}

/** Only ciphertext enters SQLite. The key is supplied separately by Workers secrets. */
export class QueueSecrets {
  constructor(
    private storage: DurableObjectStorage,
    private scope: string,
    private masterKey?: string,
  ) {
    storage.sql.exec(`CREATE TABLE IF NOT EXISTS queue_secrets (
      name TEXT PRIMARY KEY NOT NULL,
      nonce TEXT NOT NULL,
      ciphertext TEXT NOT NULL
    )`);
  }

  private async key() {
    try {
      const bytes = decode(this.masterKey ?? '');
      if (bytes.length !== 32) throw new Error();
      return await crypto.subtle.importKey('raw', bytes, 'AES-GCM', false, [
        'encrypt',
        'decrypt',
      ]);
    } catch {
      throw new ApiError(
        503,
        'Queue secret encryption key is not configured correctly',
      );
    }
  }

  private aad(name: string) {
    return encoder.encode(
      JSON.stringify(['jobd-queue-env-v1', this.scope, name]),
    );
  }

  names(): string[] {
    return this.storage.sql
      .exec<{ name: string }>('SELECT name FROM queue_secrets ORDER BY name')
      .toArray()
      .map((row) => row.name);
  }

  async set(name: string, value: unknown) {
    if (!validEnvName(name))
      throw new ApiError(400, 'Invalid environment variable name');
    if (
      typeof value !== 'string' ||
      value.includes('\0') ||
      encoder.encode(value).length > 4096
    )
      throw new ApiError(
        400,
        'Value must be a string without NUL, at most 4096 UTF-8 bytes',
      );
    const key = await this.key();
    const nonce = crypto.getRandomValues(new Uint8Array(12));
    const ciphertext = await crypto.subtle.encrypt(
      { name: 'AES-GCM', iv: nonce, additionalData: this.aad(name) },
      key,
      encoder.encode(value),
    );
    this.storage.transactionSync(() => {
      const names = this.names();
      if (!names.includes(name) && names.length >= maxVariables)
        throw new ApiError(409, 'Queue environment is limited to 64 variables');
      this.storage.sql.exec(
        `INSERT INTO queue_secrets (name, nonce, ciphertext) VALUES (?, ?, ?)
         ON CONFLICT(name) DO UPDATE SET nonce = excluded.nonce, ciphertext = excluded.ciphertext`,
        name,
        encode(nonce),
        encode(new Uint8Array(ciphertext)),
      );
    });
  }

  remove(name: string) {
    if (!validEnvName(name))
      throw new ApiError(400, 'Invalid environment variable name');
    this.storage.sql.exec('DELETE FROM queue_secrets WHERE name = ?', name);
  }

  async environment(): Promise<Record<string, string>> {
    const rows = this.storage.sql
      .exec<{ name: string; nonce: string; ciphertext: string }>(
        'SELECT * FROM queue_secrets ORDER BY name',
      )
      .toArray();
    if (!rows.length) return {};
    const key = await this.key();
    const entries: [string, string][] = [];
    try {
      for (const row of rows) {
        const plaintext = await crypto.subtle.decrypt(
          {
            name: 'AES-GCM',
            iv: decode(row.nonce),
            additionalData: this.aad(row.name),
          },
          key,
          decode(row.ciphertext),
        );
        entries.push([
          row.name,
          new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(
            plaintext,
          ),
        ]);
      }
    } catch {
      throw new ApiError(503, 'Unable to decrypt queue environment');
    }
    return Object.fromEntries(entries);
  }
}
