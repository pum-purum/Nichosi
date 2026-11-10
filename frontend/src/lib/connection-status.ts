import { writable } from 'svelte/store';

export const unavailableConnection = writable<{ id: string; request: number } | null>(null);
let requestSequence = 0;
export function connectionStatusVersion() { return requestSequence; }

/** Check database availability after failed operations, including SQL execution results. */
export async function databaseFetch(path: string, init?: RequestInit): Promise<Response> {
  const match = /^\/api\/connections\/([^/]+)\/(.*)$/.exec(path);
  const request = ++requestSequence;
  const report = () => { if (match) unavailableConnection.set({ id: decodeURIComponent(match[1]), request }); };
  let response: Response;
  try {
    response = await fetch(path, init);
  } catch (cause) {
    if (!init?.signal?.aborted) report();
    throw cause;
  }
  if (!match || init?.signal?.aborted) return response;
  if (response.ok && match[2] !== 'scripts/execute') return response;
  const body = await response.clone().json().catch(() => null);
  if (body?.code === 'disconnected' || body?.code === 'connection_failed') {
    report();
  } else if (response.status >= 500 || body?.entry?.success === false) {
    if (match[2] === 'schema') report();
    else {
      // A query can fail while the database remains reachable (for example invalid SQL).
      try {
        const probe = await fetch(`/api/connections/${match[1]}/schema`, { signal: init?.signal });
        if (!probe.ok && !init?.signal?.aborted) report();
      } catch {
        if (!init?.signal?.aborted) report();
      }
    }
  }
  return response;
}
