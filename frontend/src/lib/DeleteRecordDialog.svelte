<script lang="ts">
  import { onMount } from 'svelte';
  import type { components } from './api/schema';
  import CellValue from './CellValue.svelte';
  import { createChangeSetStore, rowDeletion } from './row-edit';
  import { highlightSQL } from './sql-highlight';

  let { connectionId, table, columns, row, editKey, cancel, saved }: {
    connectionId: string; table: string; columns: components['schemas']['Column'][]; row: unknown[]; editKey: string[];
    cancel: () => void; saved: () => Promise<void>;
  } = $props();
  let dialog: HTMLDialogElement;
  let cancelButton: HTMLButtonElement;
  const changeSet = createChangeSetStore();
  let preview = $state<components['schemas']['ChangePreview'] | null>(null);
  let error = $state('');
  let applying = $state(false);
  let disposed = false;
  const controller = new AbortController();

  async function post<T>(operation: 'preview' | 'apply'): Promise<T> {
    const response = await fetch(`/api/connections/${encodeURIComponent(connectionId)}/tables/${encodeURIComponent(table)}/changes/${operation}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify($changeSet),
      signal: AbortSignal.any([controller.signal, AbortSignal.timeout(12000)]),
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.message ?? `Request failed (${response.status})`);
    return result as T;
  }
  onMount(() => {
    dialog.showModal();
    cancelButton.focus();
    void (async () => {
      try {
        changeSet.set(rowDeletion(columns, row, editKey));
        const result = await post<components['schemas']['ChangePreview']>('preview');
        if (!disposed) preview = result;
      } catch (cause) { if (!disposed) error = cause instanceof Error ? cause.message : 'Could not preview deletion'; }
    })();
    return () => { disposed = true; controller.abort(); };
  });
  async function apply() {
    if (applying || !preview) return;
    applying = true; error = '';
    try {
      await post<components['schemas']['ApplyResult']>('apply');
      if (!disposed) await saved();
    } catch (cause) { if (!disposed) error = cause instanceof Error ? cause.message : 'Could not delete record'; }
    finally { if (!disposed) applying = false; }
  }
</script>

<dialog bind:this={dialog} aria-labelledby="delete-title" aria-describedby="delete-description" oncancel={(event) => { event.preventDefault(); if (!applying) cancel(); }}>
  <h1 id="delete-title">Delete record from {table}?</h1>
  <p id="delete-description">Confirm deletion of the following record. This action cannot be undone.</p>
  <div class="contents">
    <table aria-label="Record to delete"><tbody>
      {#each columns as column, index}<tr><th scope="row">{column.name}<small>{column.type}</small></th><td><CellValue value={row[index]} sqlType={column.type} /></td></tr>{/each}
    </tbody></table>
    <h2>SQL preview</h2>
    {#if preview}
      {#each preview.statements as statement, index}
        <pre><code>{@html highlightSQL(statement)}</code></pre>
        <p>Parameters (in placeholder order)</p><pre>{JSON.stringify(preview.parameters?.[index] ?? [], null, 2)}</pre>
      {/each}
      {#each preview.warnings as warning}<p class="warning">{warning}</p>{/each}
    {:else if !error}<p role="status">Loading SQL preview…</p>{/if}
    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </div>
  <footer><button type="button" bind:this={cancelButton} onclick={cancel} disabled={applying}>Cancel</button><button type="button" class="delete" onclick={() => void apply()} disabled={applying || !preview}>{applying ? 'Deleting…' : 'Delete record'}</button></footer>
</dialog>

<style>
  dialog { color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: 10px; padding: 24px; width: min(760px, calc(100vw - 32px)); max-height: calc(100dvh - 48px); }
  dialog::backdrop { background: #0009; }
  h1 { margin: 0; font-size: 18px; overflow-wrap: anywhere; } h2 { font-size: 14px; margin-top: 24px; }
  p { color: var(--muted); font-size: 13px; } .contents { max-height: calc(100dvh - 250px); overflow: auto; }
  table { width: 100%; border-collapse: collapse; table-layout: fixed; } th, td { text-align: left; vertical-align: top; padding: 12px; border-bottom: 1px solid var(--line); white-space: pre-wrap; overflow-wrap: anywhere; } th { width: 35%; font-size: 13px; } td { font: 13px var(--mono); }
  td :global(.cell-content) { white-space: pre-wrap; overflow: visible; overflow-wrap: anywhere; }
  td :global(.value-badges) { flex-wrap: wrap; }
  small { display: block; color: var(--muted); font: 12px var(--mono); margin-top: 4px; }
  pre { padding: 12px; background: var(--bg); white-space: pre-wrap; overflow-wrap: anywhere; font: 13px/1.6 var(--mono); } code { font: inherit; }
  code :global(.hljs-keyword) { color: var(--sql-keyword); } code :global(.hljs-string) { color: var(--sql-string); } code :global(.hljs-number) { color: var(--sql-number); }
  footer { display: flex; justify-content: flex-end; gap: 12px; padding-top: 20px; }
  button { display: inline-flex; align-items: center; justify-content: center; padding: 5px 10px; font-size: 12px; line-height: 18px; border: 1px solid var(--line); border-radius: 5px; background: var(--surface-raised); color: var(--text); cursor: pointer; } button:disabled { opacity: .45; cursor: default; } button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .delete { background: #a63232; border-color: #c34a4a; color: white; } .error { color: #f39b9b; } .warning { color: #e7ca8c; }
</style>
