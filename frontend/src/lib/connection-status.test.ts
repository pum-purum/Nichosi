import { afterEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { databaseFetch, unavailableConnection } from './connection-status';

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
afterEach(() => { vi.unstubAllGlobals(); unavailableConnection.set(null); });

describe('database availability', () => {
  it('reports unavailable schema after the database container stops', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json({ code: 'metadata_failed' }, 502)));
    const response = await databaseFetch('/api/connections/local/schema');
    expect(response.status).toBe(502);
    expect(get(unavailableConnection)?.id).toBe('local');
    expect(await response.json()).toEqual({ code: 'metadata_failed' });
  });
  it('checks connectivity after a row request fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(json({ code: 'query_failed' }, 502)).mockResolvedValueOnce(json({}, 502)));
    await databaseFetch('/api/connections/local/tables/demo/rows');
    expect(get(unavailableConnection)?.id).toBe('local');
  });
  it('keeps the connection active for SQL errors while schema remains reachable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(json({ entry: { success: false } })).mockResolvedValueOnce(json({ tables: [] })));
    await databaseFetch('/api/connections/local/scripts/execute');
    expect(get(unavailableConnection)).toBeNull();
  });
  it('reports connection loss in a failed SQL execution result', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(json({ entry: { success: false } })).mockRejectedValueOnce(new TypeError('Failed to fetch')));
    await databaseFetch('/api/connections/local/scripts/execute');
    expect(get(unavailableConnection)?.id).toBe('local');
  });
  it('ignores cancelled requests', async () => {
    const controller = new AbortController(); controller.abort();
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new DOMException('Aborted', 'AbortError')));
    await expect(databaseFetch('/api/connections/local/schema', { signal: controller.signal })).rejects.toThrow();
    expect(get(unavailableConnection)).toBeNull();
  });
});
