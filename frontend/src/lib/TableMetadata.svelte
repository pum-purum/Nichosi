<script lang="ts">
  import { groupMetadataKeys, metadataLabels } from './table-metadata';
  import ObjectDefinition from './ObjectDefinition.svelte';
  import type { components } from './api/schema';

  let { connectionId, table, relationType = 'table' }: { connectionId: string; table: string; relationType?: 'table' | 'view' } = $props();
  type Metadata = components['schemas']['TableMetadata'];
  let metadata = $state<Metadata | null>(null);
  let loading = $state(true);
  let error = $state('');
  let attempt = $state(0);
  const groups = $derived(metadata ? [
    { title: 'Columns', rows: metadata.columns, preferred: ['Name', 'Type', 'Default', 'Nullable', 'Extra', 'Key'] },
    ...(relationType === 'table' ? [
      { title: 'Keys and indexes', rows: groupMetadataKeys(metadata.indexes), preferred: ['Name', 'Columns', 'Kind', 'Type'] },
      { title: 'Foreign keys', rows: groupMetadataKeys(metadata.foreignKeys, true), preferred: ['Name', 'Columns', 'References', 'On update', 'On delete'] },
      { title: 'Triggers', rows: metadata.triggers, preferred: ['Name', 'Timing', 'Event', 'Statement'] },
    ] : []),
  ] : []);
  $effect(() => {
    const controller = new AbortController();
    const path = `/api/connections/${encodeURIComponent(connectionId)}/${relationType === 'view' ? 'views' : 'tables'}/${encodeURIComponent(table)}/metadata`;
    attempt;
    metadata = null; loading = true; error = '';
    void (async () => {
      try {
        const response = await fetch(path, { signal: controller.signal });
        const body = await response.json();
        if (!response.ok) throw new Error(body.message || `Request failed (${response.status})`);
        if (!controller.signal.aborted) metadata = body as Metadata;
      } catch (cause) {
        if (!controller.signal.aborted) error = cause instanceof Error ? cause.message : 'Could not load metadata';
      } finally {
        if (!controller.signal.aborted) loading = false;
      }
    })();
    return () => controller.abort();
  });
</script>

<section class="table-metadata" aria-label={`Metadata for ${table}`} aria-busy={loading}>
  <header><h1>{table}</h1>{#if relationType === 'table'}<span>Table information · Read-only</span>{/if}</header>
  {#if loading}
    <p role="status">Loading metadata…</p>
  {:else if error}
    <div role="alert"><p>{error}</p><button onclick={() => attempt++}>Retry</button></div>
  {:else if metadata}
    {#each metadata.warnings as warning}<p class="metadata-warning" role="status">{warning}</p>{/each}
    <h2>Overview</h2>
    <p class="estimate-note">{relationType === 'view' ? 'Views have no separate table storage. Properties reflect the last load.' : 'Row counts and storage sizes are server estimates. Metadata reflects the last load.'}</p>
    <dl>{#each Object.entries(metadata.details) as [label, value]}<div><dt>{label}</dt><dd>{value || '—'}</dd></div>{/each}</dl>
    {#each groups as group}
      <section class="metadata-group" aria-label={group.title}>
        <h2>{group.title} <small>{group.rows.length}</small></h2>
        {#if group.rows.length}
          {@const labels = metadataLabels(group.rows, group.preferred)}
          <!-- svelte-ignore a11y_no_noninteractive_tabindex (Scrollable metadata needs keyboard access.) -->
          <div class="metadata-scroll" role="region" aria-label={group.title} tabindex="0"><table><thead><tr>{#each labels as label}<th scope="col">{label}</th>{/each}</tr></thead><tbody>{#each group.rows as row}<tr>{#each labels as label}<td>{row[label] ?? '—'}</td>{/each}</tr>{/each}</tbody></table></div>
        {:else}<p>No entries returned. Any availability limits are listed above.</p>{/if}
      </section>
    {/each}
    {#if metadata.definition}<ObjectDefinition definition={metadata.definition} name={table} isView={relationType === 'view'} sqlMode={metadata.details['SQL mode']} />{:else}<h2>SQL definition</h2><p>CREATE {relationType === 'view' ? 'VIEW' : 'TABLE'} is unavailable. See availability warnings above.</p>{/if}
  {/if}
</section>

<style>
  .table-metadata { flex: 1; min-height: 0; overflow: auto; margin-top: 14px; padding: 24px; border: 1px solid var(--line); border-bottom: 0; border-radius: 9px 9px 0 0; background: var(--surface); }
  header { display: flex; align-items: center; flex-wrap: wrap; gap: 14px; }
  h1 { margin: 0; overflow-wrap: anywhere; font-size: 22px; }
  header > span, .estimate-note, p { color: var(--muted); font-size: 13px; line-height: 1.6; }
  button { padding: 7px 12px; border: 1px solid var(--line); border-radius: 6px; background: var(--surface-raised); cursor: pointer; }
  button:disabled { opacity: .5; cursor: default; }
  h2 { margin: 26px 0 12px; font-size: 14px; }
  h2 small { margin-left: 8px; color: var(--muted); font-weight: normal; }
  dl { display: grid; grid-template-columns: repeat(auto-fit, minmax(210px, 1fr)); gap: 14px; }
  dl > div { min-width: 0; padding: 12px; border: 1px solid var(--line-soft); border-radius: 6px; }
  dt { color: var(--muted); font-size: 12px; }
  dd { margin: 7px 0 0; white-space: pre-wrap; overflow-wrap: anywhere; font-size: 13px; }
  .metadata-scroll { overflow: auto; border: 1px solid var(--line); border-radius: 6px; }
  table { width: 100%; border-collapse: collapse; font-size: 13px; text-align: left; }
  th, td { padding: 10px 12px; border-bottom: 1px solid var(--line-soft); vertical-align: top; min-width: 110px; white-space: pre-wrap; overflow-wrap: anywhere; max-width: 480px; }
  th { background: var(--surface-raised); color: var(--muted); font-weight: 500; }
  tr:last-child td { border-bottom: 0; }
  .metadata-warning { color: #e7be75; }
</style>
