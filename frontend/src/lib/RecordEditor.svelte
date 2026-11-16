<script lang="ts">
  import { databaseFetch } from './connection-status';

  import { ArrowLeft, Save } from '@lucide/svelte';
  import { onDestroy } from 'svelte';
  import type { components } from './api/schema';
  import { createChangeSetStore, draftValue, editableColumn, rowChanges, insertableColumn, insertionDraft, rowFieldErrors } from './row-edit';
  import MemberSelect from './MemberSelect.svelte';
  import { columnOptions } from './column-options';
  import { highlightSQL } from './sql-highlight';

  let { connectionId, table, columns, row, editKey, cancel, saved, inserting = false }: {
    connectionId: string; table: string; columns: components['schemas']['Column'][]; row: unknown[]; editKey: string[];
    inserting?: boolean; cancel: () => void; saved: () => Promise<void>;
  } = $props();
  const changeSet = createChangeSetStore();
  // Each editor instance owns the original row snapshot.
  // svelte-ignore state_referenced_locally
  let draft = $state(inserting ? insertionDraft(columns) : row.map(draftValue));
  let preview = $state<components['schemas']['ChangePreview'] | null>(null);
  let error = $state('');
  let submitted = $state(false);
  const fieldErrors = $derived(rowFieldErrors(columns, row, draft, inserting));
  let previewing = $state(false);
  let applying = $state(false);
  let revision = 0;
  let previewRevision = -1;
  let disposed = false;
  let timer: ReturnType<typeof setTimeout>;
  onDestroy(() => { disposed = true; revision++; clearTimeout(timer); });

  async function post<T>(operation: string, body: components['schemas']['ChangeSet']): Promise<T> {
    const response = await databaseFetch(`/api/connections/${encodeURIComponent(connectionId)}/tables/${encodeURIComponent(table)}/changes/${operation}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal: AbortSignal.timeout(12000),
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.message ?? `Request failed (${response.status})`);
    return result as T;
  }
  $effect(() => {
    const current = draft.map(value => ({ ...value }));
    const attempt = ++revision;
    clearTimeout(timer);
    preview = null; previewRevision = -1; error = ''; previewing = false;
    try {
      const changes = rowChanges(columns, row, current, editKey, inserting);
      changeSet.set(changes);
      if (!changes.changes.length) return;
      previewing = true;
      timer = setTimeout(async () => {
        try {
          const result = await post<components['schemas']['ChangePreview']>('preview', changes);
          if (!disposed && attempt === revision) { preview = result; previewRevision = attempt; }
        } catch (cause) {
          if (!disposed && attempt === revision) error = cause instanceof Error ? cause.message : 'Could not preview changes';
        } finally { if (!disposed && attempt === revision) previewing = false; }
      }, 250);
    } catch (cause) {
      changeSet.set({ changes: [] });
      if (!rowFieldErrors(columns, row, current, inserting).some(Boolean)) error = cause instanceof Error ? cause.message : 'Invalid value';
    }
  });
  async function apply() {
    submitted = true;
    if (fieldErrors.some(Boolean)) return;
    if (applying || previewing || !preview || previewRevision !== revision || !$changeSet.changes.length) return;
    applying = true; error = '';
    try {
      await post<components['schemas']['ApplyResult']>('apply', $changeSet);
      if (!disposed) await saved();
    } catch (cause) { if (!disposed) error = cause instanceof Error ? cause.message : 'Could not apply changes'; }
    finally { if (!disposed) applying = false; }
  }
</script>

<section class="record-editor" aria-label={inserting ? "Add database record" : "Edit database record"} aria-busy={applying}>
  <header><button onclick={cancel} disabled={applying}><ArrowLeft size={14} />Back to data</button><h1>{inserting ? 'New record' : 'Edit record'} <span>{table}</span></h1></header>
  <form onsubmit={(event) => { event.preventDefault(); void apply(); }}>
    <fieldset disabled={applying}>
      {#each columns as column, index}
        {@const options = columnOptions(column.type)}
        <div class="record-field">
          <label for={`record-field-${index}`}>{column.name}{#if inserting && !column.nullable && !draft[index].useDefault && insertableColumn(column)}<span class="required-marker" aria-label="Required">*</span>{/if}<small>{column.type}{editKey.includes(column.name) ? ' · Row key' : ''}{column.primaryKey ? inserting ? ' · Primary key' : ' · Read-only primary key' : ''}{inserting && column.autoIncrement ? ' · Auto increment' : ''}{column.generated ? ' · Generated' : ''}{!column.primaryKey && !column.generated && !(inserting ? insertableColumn(column) : editableColumn(column, row[index])) ? ' · Read-only binary value' : ''}</small></label>
          <div class="field-value">
            {#if options}
              <MemberSelect id={`record-field-${index}`} name={column.name} {options} bind:value={draft[index].text} disabled={(inserting && draft[index].useDefault) || draft[index].isNull || !(inserting ? insertableColumn(column) : editableColumn(column, row[index]))} invalid={submitted && !!fieldErrors[index]} describedby={submitted && fieldErrors[index] ? `record-error-${index}` : undefined} />
            {:else}
            <textarea id={`record-field-${index}`} rows={1} value={inserting && column.autoIncrement && draft[index].useDefault ? '' : draft[index].text} oninput={(event) => { draft[index].text = event.currentTarget.value; }} disabled={(inserting && draft[index].useDefault) || draft[index].isNull || !(inserting ? insertableColumn(column) : editableColumn(column, row[index]))} placeholder={inserting && column.autoIncrement && draft[index].useDefault && column.nextAutoIncrementValue !== undefined ? `≈ ${column.nextAutoIncrementValue}` : undefined} spellcheck="false" aria-invalid={submitted && !!fieldErrors[index]} aria-describedby={[submitted && fieldErrors[index] ? `record-error-${index}` : '', inserting && column.autoIncrement && draft[index].useDefault && column.nextAutoIncrementValue !== undefined ? `record-estimate-${index}` : ''].filter(Boolean).join(' ') || undefined} aria-label={`${column.name} value`}></textarea>
            {/if}
            {#if inserting && column.autoIncrement && draft[index].useDefault && column.nextAutoIncrementValue !== undefined}<p class="identity-hint" id={`record-estimate-${index}`}>Estimated ID; the database assigns the actual value on insert.</p>{/if}
            {#if submitted && fieldErrors[index]}<p class="field-error" id={`record-error-${index}`} role="alert">{fieldErrors[index]}</p>{/if}
          </div>
          <div class="field-options">
            {#if inserting && column.autoIncrement}<label class="null-toggle"><input type="checkbox" bind:checked={draft[index].useDefault} disabled={!insertableColumn(column)} />Generate automatically</label>{:else if inserting && column.defaultValue !== undefined}<label class="null-toggle"><input type="checkbox" bind:checked={draft[index].useDefault} disabled={!insertableColumn(column)} />Default <code class="default-value">{column.defaultValue}</code></label>{/if}
            {#if column.nullable}<label class="null-toggle"><input type="checkbox" bind:checked={draft[index].isNull} disabled={(inserting && draft[index].useDefault) || !(inserting ? insertableColumn(column) : editableColumn(column, row[index]))} />NULL</label>{:else if !inserting}<span class="required">Required</span>{/if}
          </div>
          {#if !inserting && editableColumn(column, row[index]) && (draft[index].isNull !== (row[index] === null) || (!draft[index].isNull && draft[index].text !== draftValue(row[index]).text))}<span class="modified">Modified</span>{/if}
        </div>
      {/each}
    </fieldset>
    <section class="sql-preview" aria-label="SQL preview">
      <h2>SQL preview</h2>
      <p role="status">{previewing ? 'Updating preview…' : inserting ? 'Default fields are omitted. The database evaluates defaults when changes are applied.' : !$changeSet.changes.length ? 'Edit a value to preview the update.' : 'Only modified columns are included. Row key values identify the record.'}</p>
      {#if preview}
        {#each preview.statements as statement, index}
          <pre><code>{@html highlightSQL(statement)}</code></pre>
          <p class="parameter-label">Parameters (in placeholder order)</p><pre class="parameters">{JSON.stringify(preview.parameters?.[index] ?? [], null, 2)}</pre>
        {/each}
        {#each preview.warnings as warning}<p class="warning">{warning}</p>{/each}
      {/if}
    </section>
    {#if submitted && error}<p class="edit-error" role="alert">{error}</p>{/if}
    <footer><button type="button" onclick={cancel} disabled={applying}>Cancel</button><button class="toolbar-button toolbar-button-primary" type="submit" disabled={applying || previewing || (!error && !fieldErrors.some(Boolean) && (!preview || !$changeSet.changes.length))}><Save size={14} />{applying ? 'Applying…' : 'Apply changes'}</button></footer>
  </form>
</section>

<style>
  .record-editor { height: 100%; overflow: auto; padding: 24px; }
  header { display: flex; flex-wrap: wrap; align-items: center; gap: 24px; margin-bottom: 24px; }
  h1 { font-size: 19px; margin: 0; } h1 span { color: var(--muted); font-weight: 400; margin-left: 8px; }
  button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; padding: 5px 10px; font-size: 12px; line-height: 18px; border: 1px solid var(--line); border-radius: 5px; background: var(--surface-raised); cursor: pointer; }
  button:disabled { opacity: .45; cursor: default; }
  button:focus-visible, textarea:focus-visible, input:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  form { max-width: 1100px; } fieldset { padding: 0; margin: 0; border: 0; min-width: 0; }
  .record-field { display: grid; grid-template-columns: minmax(140px, 220px) minmax(180px, 1fr) minmax(120px, 240px) 65px; gap: 14px; align-items: center; padding: 12px 0; border-bottom: 1px solid var(--line-soft); }
  label { overflow-wrap: anywhere; font-size: 14px; } small { display: block; color: var(--muted); font: 12px var(--mono); margin-top: 5px; }
  textarea { width: 100%; min-height: 37px; resize: vertical; padding: 8px 10px; border: 1px solid var(--line); border-radius: 5px; background: var(--bg); color: var(--text); font: 13px/1.5 var(--mono); }
  textarea[aria-invalid="true"] { border-color: #f39b9b; }
  .field-error { color: #f39b9b; margin: 6px 0 0; }
  textarea::placeholder { color: var(--muted); opacity: 1; }
  .identity-hint { margin: 6px 0 0; font-size: 12px; }
  textarea:disabled { opacity: .5; } .null-toggle { display: flex; align-items: center; gap: 6px; font: 12px var(--mono); } input { accent-color: var(--accent); }
  .default-value { white-space: pre-wrap; overflow-wrap: anywhere; }
  .field-value { min-width: 0; } .field-options { display: grid; gap: 8px; }
  .required-marker { color: #f39b9b; margin-left: 4px; }
  .required { color: var(--dim); font-size: 12px; } .modified { color: var(--accent); font-size: 12px; }
  .sql-preview { margin-top: 28px; } h2 { font-size: 14px; } p { color: var(--muted); font-size: 13px; }
  pre { overflow: auto; padding: 16px; border: 1px solid var(--line); border-radius: 6px; background: var(--surface); font: 13px/1.65 var(--mono); white-space: pre-wrap; overflow-wrap: anywhere; }
  code { font: inherit; } code :global(.hljs-keyword) { color: var(--sql-keyword); } code :global(.hljs-string) { color: var(--sql-string); } code :global(.hljs-number) { color: var(--sql-number); }
  .parameter-label { margin-bottom: 6px; } .parameters { margin-top: 0; color: var(--muted); } .warning { color: #e7ca8c; } .edit-error { color: #f39b9b; }
  footer { display: flex; justify-content: flex-end; gap: 12px; padding: 20px 0; }
  @media (max-width: 800px) { .record-editor { padding: 16px; } .record-field { grid-template-columns: 1fr 80px; } .record-field > label:first-child { grid-column: 1 / -1; } .modified { grid-column: 1 / -1; } }
</style>
