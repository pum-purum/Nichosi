import { afterEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { createScriptHistory } from './script-history';

function deferred() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>(done => { resolve = done; });
  return { promise, resolve };
}
afterEach(() => vi.unstubAllGlobals());
describe('script history sidebar state', () => {
  it('keeps the newest refresh when an older response arrives last', async () => {
    const old = deferred(); const recent = deferred();
    vi.stubGlobal('fetch', vi.fn().mockReturnValueOnce(old.promise).mockReturnValueOnce(recent.promise));
    const history = createScriptHistory('connection/a');
    const first = history.load(); const second = history.load();
    recent.resolve(Response.json([{ id: 'recent', sql: 'SELECT 2' }])); await second;
    old.resolve(Response.json([{ id: 'old', sql: 'SELECT 1' }])); await first;
    expect(get(history).entries[0].id).toBe('recent');
    expect(get(history).loading).toBe(false);
  });
  it('clears entries and selection, but blocks clearing during execution', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(Response.json([{ id: 'run' }])).mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetch);
    const history = createScriptHistory('connection/a'); await history.load(); history.select('run');
    history.setExecuting(true); await history.clear(); expect(fetch).toHaveBeenCalledTimes(1);
    history.setExecuting(false); await history.clear();
    expect(get(history).entries).toEqual([]); expect(get(history).selectedId).toBe('');
    expect(fetch.mock.calls[1][0]).toContain('connection%2Fa'); expect(fetch.mock.calls[1][1].method).toBe('DELETE');
  });
  it('reports errors and ignores late responses after leaving the console', async () => {
    const pending = deferred();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(Response.json({ message: 'Unavailable' }, { status: 503 })).mockReturnValueOnce(pending.promise));
    const history = createScriptHistory('a'); await history.load(); expect(get(history).error).toBe('Unavailable');
    const load = history.load(); history.dispose(); pending.resolve(Response.json([{ id: 'late' }])); await load;
    expect(get(history).entries).toEqual([]);
  });
});
