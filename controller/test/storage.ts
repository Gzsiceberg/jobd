import { DatabaseSync, type SQLInputValue } from 'node:sqlite';
import { afterEach } from 'vitest';
import { Scheduler } from '../src/jobs/scheduler';

const connections: DatabaseSync[] = [];
afterEach(() => {
  for (const connection of connections.splice(0)) connection.close();
});

/** Node SQLite test double for the Cloudflare DurableObjectStorage API. */
export function schedulerStorage() {
  const sqlite = new DatabaseSync(':memory:');
  connections.push(sqlite);
  const storage = {
    sql: {
      exec<T extends Record<string, SqlStorageValue>>(
        query: string,
        ...bindings: SqlStorageValue[]
      ) {
        const values = bindings.map((value) =>
          value instanceof ArrayBuffer ? new Uint8Array(value) : value,
        ) as SQLInputValue[];
        const rows = sqlite.prepare(query).all(...values) as T[];
        return { toArray: () => rows } as SqlStorageCursor<T>;
      },
    },
    transactionSync<T>(action: () => T): T {
      sqlite.exec('BEGIN');
      try {
        const result = action();
        sqlite.exec('COMMIT');
        return result;
      } catch (error) {
        sqlite.exec('ROLLBACK');
        throw error;
      }
    },
  } as unknown as DurableObjectStorage;
  return { sqlite, storage, scheduler: new Scheduler(storage) };
}
