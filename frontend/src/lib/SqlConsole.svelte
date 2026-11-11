<script lang="ts">
  import { databaseFetch } from './connection-status';

  import { onMount } from 'svelte';
  import { EditorState } from '@codemirror/state';
  import { EditorView, keymap, lineNumbers, highlightActiveLine, drawSelection } from '@codemirror/view';
  import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
  import { sql as sqlLanguage, MySQL, PostgreSQL, SQLite } from '@codemirror/lang-sql';
  import { Play } from '@lucide/svelte';
  import { sqlEditorHighlighting } from './sql-editor-highlight';
  import { sqlDraft, saveSqlDraft } from './sql-drafts';
  import { sqlStatementError } from './sql-statement';
  import { cellValue } from './cell-value';
  import type { ScriptHistory } from './script-history';
  import type { components } from './api/schema';

  type Execution = components['schemas']['ScriptExecution'];
  type Entry = components['schemas']['ScriptHistoryEntry'];
  let { connection, history: scriptHistory }: { connection: components['schemas']['Connection']; history: ScriptHistory } = $props();
  let editorHost: HTMLDivElement;
  let editor: EditorView;
  let source = $state('SELECT 1;');
  let execution = $state<Execution | null>(null);
  let error = $state('');
  let busy = $state(false);
  const lifecycle = new AbortController();
  const base = $derived(`/api/connections/${encodeURIComponent(connection.id)}/scripts`);
  const statementError = $derived(sqlStatementError(source, connection.driver));
  let alive = true;
  $effect(() => { scriptHistory.setExecuting(busy); });
  function saveDraft() { saveSqlDraft(connection.id, source); }
  async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const response = await databaseFetch(`${base}/${path}`, { ...init, signal: lifecycle.signal, headers: { 'Content-Type': 'application/json' } });
    if (!response.ok) { const body = await response.json().catch(() => null); throw new Error(body?.message ?? `Request failed (${response.status})`); }
    return response.status === 204 ? undefined as T : response.json();
  }
  async function execute() {
    if (busy || !source.trim() || statementError) return;
    busy = true; error = ''; execution = null;
    try {
      const result = await request<Execution>('execute', { method: 'POST', body: JSON.stringify({ sql: source, atomic: true }) });
      if (alive) { execution = result; scriptHistory.select(result.entry.id); await scriptHistory.load(); }
    } catch (cause) { if (alive) error = cause instanceof Error ? cause.message : 'Execution failed'; }
    finally { if (alive) busy = false; }
  }
  export function repeat(entry: Entry) {
    if (busy) return;
    editor.dispatch({ changes: { from: 0, to: editor.state.doc.length, insert: entry.sql } });
    saveDraft(); execution = null; error = ''; editor.focus();
  }
  function cell(value: unknown): string { return cellValue(value, '').text; }
  onMount(() => {
    const draft = sqlDraft(connection.id); source = draft.sql;
    editor = new EditorView({ parent: editorHost, state: EditorState.create({ doc: source, extensions: [
      lineNumbers(), history(), highlightActiveLine(), drawSelection(), sqlEditorHighlighting,
      sqlLanguage({ dialect: connection.driver === 'mysql' ? MySQL : connection.driver === 'postgres' ? PostgreSQL : SQLite }),
      keymap.of([{ key: 'Mod-Enter', run: () => { void execute(); return true; } }, ...defaultKeymap, ...historyKeymap]),
      EditorView.contentAttributes.of({ 'aria-label': 'SQL script', spellcheck: 'false' }),
      EditorView.updateListener.of(update => { if (update.docChanged) { source = update.state.doc.toString(); saveDraft(); } }),
      EditorView.theme({ '&': { height: '100%', backgroundColor: 'var(--bg, #191b1f)', color: 'var(--sql-text)' }, '.cm-scroller': { overflow: 'auto', fontFamily: 'var(--mono)', fontSize: '14px' }, '.cm-content': { minHeight: '180px', caretColor: 'var(--accent)' }, '.cm-gutters': { backgroundColor: 'transparent', color: '#777f8b', border: 'none' }, '.cm-activeLine': { backgroundColor: '#ffffff05' }, '&.cm-focused .cm-selectionBackground, .cm-selectionBackground': { backgroundColor: 'color-mix(in srgb, var(--accent) 21%, transparent)' }, '.cm-cursor': { borderLeftColor: 'var(--accent)' } }, { dark: true }),
    ] }) });
    return () => { scriptHistory.setExecuting(false); alive = false; lifecycle.abort(); editor.destroy(); };
  });
</script>

<section class="sql-console" aria-label="SQL console" aria-busy={busy}>
  <header><h1>SQL console</h1></header>
  <div class="sql-editor" bind:this={editorHost}></div>
  <div class="script-toolbar"><button class="toolbar-button toolbar-button-primary" disabled={busy || !!statementError || !source.trim()} onclick={execute} title="Execute (Ctrl/⌘+Enter)" aria-keyshortcuts="Control+Enter Meta+Enter"><Play size={15} />{busy ? 'Running…' : 'Execute'}</button></div>
  {#if source.trim() && statementError}<p class="mode-warning" role="status">{statementError}</p>{/if}
  {#if error}<p class="script-error" role="alert">{error}</p>{/if}
  {#if execution}<section class="script-results" aria-label="Script results"><span class="sr-only" role="status">{execution.entry.success ? 'Executed successfully' : 'Execution failed'}</span>{#if source !== execution.entry.sql}<p>These results belong to the previously executed script.</p>{/if}{#if execution.entry.error}<p class="script-error" role="alert">{execution.entry.error}</p>{/if}
    {#each execution.results as result}<article>{#if result.columns.length}<div class="script-grid"><table><thead><tr>{#each result.columns as column}<th>{column}</th>{/each}</tr></thead><tbody>{#each result.rows as row}<tr>{#each row as value}<td class:null-value={value === null} title={cell(value)}>{cell(value)}</td>{/each}</tr>{/each}</tbody></table></div>{#if result.truncated}<p class="result-limit" role="status">Result limited to {execution.entry.rowLimit} records.</p>{/if}{:else}<p>{result.rowsAffected} row(s) affected</p>{/if}</article>{/each}
  </section>{/if}
</section>

<style>
.sql-console{height:100%;overflow:auto;padding:22px;display:flex;flex-direction:column;gap:14px;min-width:0;color:var(--text,#dce0e5)}
header,.script-toolbar{display:flex;align-items:center;gap:12px}header{justify-content:space-between}h1{font-size:19px;margin:0}p{font-size:12px;color:var(--muted,#9aa2ad)}.sql-editor{min-height:200px;height:250px;flex-shrink:0;border:1px solid #ffffff20;border-radius:7px;overflow:hidden}.script-toolbar{justify-content:flex-end;flex-wrap:wrap}.script-results{border:1px solid #ffffff18;border-radius:7px;padding:15px}.mode-warning{color:#e9be74;background:#e9be7410;padding:12px;border:1px solid #e9be7440;border-radius:5px}.script-error{color:#f6a0a0;white-space:pre-wrap;overflow-wrap:anywhere}.script-grid{display:block;overflow:auto;max-height:420px}.result-limit{margin:0;padding:12px 12px 0;line-height:18px}table{border-collapse:separate;border-spacing:0;width:100%;font-size:13px;text-align:left}th,td{padding:9px 12px;border-bottom:1px solid #ffffff12;max-width:500px;overflow:hidden;text-overflow:ellipsis;white-space:pre}th{background:var(--surface-raised,#222629);position:sticky;top:0;z-index:1}.null-value{color:#999;font-style:italic}
.sr-only{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip-path:inset(50%);white-space:nowrap;border:0}
</style>
