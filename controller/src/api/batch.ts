import { ApiError } from './errors';

// Each target owns its transaction. A rejected target never blocks later ones.
export function batch<T>(
  ids: string[],
  operation: (id: string) => T,
  reverse = false,
) {
  const unique = [...new Set(ids)];
  const succeeded: T[] = [];
  const failed: { id: string; error: string }[] = [];
  for (const id of reverse ? unique.reverse() : unique) {
    try {
      succeeded.push(operation(id));
    } catch (error) {
      if (!(error instanceof ApiError)) console.error(error);
      failed.push({
        id,
        error:
          error instanceof ApiError ? error.message : 'Internal server error',
      });
    }
  }
  if (reverse) {
    succeeded.reverse();
    failed.reverse();
  }
  return { succeeded, failed };
}
