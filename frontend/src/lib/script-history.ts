import { databaseFetch } from './connection-status';
import { get, writable } from 'svelte/store';
import type { components } from './api/schema';

type Entry = components['schemas']['ScriptHistoryEntry'];
export function createScriptHistory(connectionId: string) {
  const state = writable({ entries: [] as Entry[], loading: false, executing: false, error: '', selectedId: '' });
  let revision = 0;
  let pending: AbortController | undefined;
  const url = `/api/connections/${encodeURIComponent(connectionId)}/scripts/history`;
  async function refresh(clear = false) {
    const current = ++revision;
    pending?.abort();
    const controller = new AbortController(); pending = controller;
    state.update(value => ({ ...value, loading: true, error: '' }));
    try {
      const response = await databaseFetch(url, { method: clear ? 'DELETE' : 'GET', signal: controller.signal });
      if (!response.ok) { const body = await response.json().catch(() => null); throw new Error(body?.message ?? `Request failed (${response.status})`); }
      const entries: Entry[] = clear ? [] : await response.json();
      if (current === revision) state.update(value => ({ ...value, entries, selectedId: clear ? '' : value.selectedId }));
    } catch (cause) {
      if (current === revision && !controller.signal.aborted) state.update(value => ({ ...value, error: cause instanceof Error ? cause.message : 'Could not load history' }));
    } finally {
      if (current === revision) state.update(value => ({ ...value, loading: false }));
    }
  }
  return {
    subscribe: state.subscribe,
    load: () => refresh(),
    clear: () => { const value = get(state); if (!value.executing && !value.loading) return refresh(true); },
    setExecuting: (executing: boolean) => state.update(value => ({ ...value, executing })),
    select: (selectedId: string) => state.update(value => ({ ...value, selectedId })),
    dispose: () => { revision++; pending?.abort(); },
  };
}
export type ScriptHistory = ReturnType<typeof createScriptHistory>;
