<script lang="ts">
  import { onMount } from 'svelte';
  import { EditorState } from '@codemirror/state';
  import { EditorView, keymap, lineNumbers, highlightActiveLine, drawSelection } from '@codemirror/view';
  import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
  import { sql as sqlLanguage, MySQL, PostgreSQL, SQLite } from '@codemirror/lang-sql';
  import { Play } from '@lucide/svelte';
  import { sqlEditorHighlighting } from './sql-editor-highlight';
  import { sqlDraft, saveSqlDraft } from './sql-drafts';
  import { cellValue } from './cell-value';
  import type { ScriptHistory } from './script-history';
  import type { components } from './api/schema';

  type Execution = components['schemas']['ScriptExecution'];
  type Entry = components['schemas']['ScriptHistoryEntry'];
  let { connection, history: scriptHistory }: { connection: components['schemas']['Connection']; history: ScriptHistory } = $props();
  let editorHost: HTMLDivElement;
  let editor: EditorView;
  let source = $state('SELECT 1;');
  let rowLimit = $state(200);
  let atomic = $state(true);
  let execution = $state<Execution | null>(null);
  let error = $state('');
  let busy = $state(false);
  const lifecycle = new AbortController();
  const base = $derived(`/api/connections/${encodeURIComponent(connection.id)}/scripts`);
  let alive = true;
  $effect(() => { scriptHistory.setExecuting(busy); });
  function saveDraft() { saveSqlDraft(connection.id, source, rowLimit, atomic); }
  async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const response = await fetch(`${base}/${path}`, { ...init, signal: lifecycle.signal, headers: { 'Content-Type': 'application/json' } });
    if (!response.ok) { const body = await response.json().catch(() => null); throw new Error(body?.message ?? `Request failed (${response.status})`); }
    return response.status === 204 ? undefined as T : response.json();
  }
  async function execute() {
    if (busy || !source.trim()) return;
    if (!Number.isInteger(rowLimit) || rowLimit < 1 || rowLimit > 1000) { error = 'Choose a row limit between 1 and 1000.'; return; }
    busy = true; error = ''; execution = null;
    try {
      const result = await request<Execution>('execute', { method: 'POST', body: JSON.stringify({ sql: source, rowLimit, atomic }) });
      if (alive) { execution = result; scriptHistory.select(result.entry.id); await scriptHistory.load(); }
    } catch (cause) { if (alive) error = `${cause instanceof Error ? cause.message : 'Execution failed'}. If the connection was interrupted, check database state before repeating.`; }
    finally { if (alive) busy = false; }
  }
  export function repeat(entry: Entry) {
    if (busy) return;
    editor.dispatch({ changes: { from: 0, to: editor.state.doc.length, insert: entry.sql } });
    rowLimit = entry.rowLimit; atomic = entry.atomic; saveDraft(); execution = null; error = ''; editor.focus();
  }
  function cell(value: unknown): string { return cellValue(value, '').text; }
  onMount(() => {
    const draft = sqlDraft(connection.id); source = draft.sql; rowLimit = draft.rowLimit; atomic = draft.atomic;
    editor = new EditorView({ parent: editorHost, state: EditorState.create({ doc: source, extensions: [
      lineNumbers(), history(), highlightActiveLine(), drawSelection(), sqlEditorHighlighting,
      sqlLanguage({ dialect: connection.driver === 'mysql' ? MySQL : connection.driver === 'postgres' ? PostgreSQL : SQLite }),
      keymap.of([{ key: 'Mod-Enter', run: () => { void execute(); return true; } }, ...defaultKeymap, ...historyKeymap]),
      EditorView.contentAttributes.of({ 'aria-label': 'SQL script', spellcheck: 'false' }),
      EditorView.updateListener.of(update => { if (update.docChanged) { source = update.state.doc.toString(); saveDraft(); } }),
      EditorView.theme({ '&': { height: '100%', backgroundColor: 'var(--bg, #191b1f)', color: 'var(--sql-text)' }, '.cm-scroller': { overflow: 'auto', fontFamily: 'monospace', fontSize: '14px' }, '.cm-content': { minHeight: '180px', caretColor: '#83c96f' }, '.cm-gutters': { backgroundColor: 'transparent', color: '#777f8b', border: 'none' }, '.cm-activeLine': { backgroundColor: '#ffffff05' }, '&.cm-focused .cm-selectionBackground, .cm-selectionBackground': { backgroundColor: '#83c96f35' }, '.cm-cursor': { borderLeftColor: '#83c96f' } }, { dark: true }),
    ] }) });
    return () => { scriptHistory.setExecuting(false); alive = false; lifecycle.abort(); editor.destroy(); };
  });
</script>

<section class="sql-console" aria-label="SQL console" aria-busy={busy}>
  <header><div><h1>SQL console</h1><span>{connection.database} · {connection.readonly ? 'Read-only' : 'Read-write'}{connection.driver === 'mysql' && atomic ? ' · Atomic console: read-only queries' : ''}</span></div></header>
  <div class="sql-editor" bind:this={editorHost}></div>
  <div class="script-toolbar"><label>Rows per result <input type="number" min="1" max="1000" bind:value={rowLimit} disabled={busy} oninput={saveDraft} onchange={saveDraft} /></label><span>30 s timeout · Ctrl/⌘+Enter to execute</span><button class="toolbar-button toolbar-button-primary" disabled={busy || !source.trim() || !Number.isInteger(rowLimit) || rowLimit < 1 || rowLimit > 1000} onclick={execute}><Play size={15} />{busy ? 'Running…' : 'Execute'}</button></div>
  <label class="script-mode"><input type="checkbox" checked={!atomic} disabled={busy || connection.readonly} onchange={(event) => { atomic = !event.currentTarget.checked; saveDraft(); }} />Allow partial execution (DDL and writes without a shared transaction)</label>
  {#if !atomic}<p class="mode-warning" role="status">Partial execution is enabled. Earlier statements may remain committed if a later statement fails. Check database state before repeating failed scripts.</p>{:else if connection.driver === 'mysql' && !connection.readonly}<p>MySQL/MariaDB atomic mode supports read-only queries. Enable partial execution to run writes and DDL.</p>{/if}
  {#if error}<p class="script-error" role="alert">{error}</p>{/if}
  {#if execution}<section class="script-results" aria-label="Script results"><h2>{execution.entry.success ? 'Executed successfully' : 'Execution failed'} · {execution.entry.durationMs} ms</h2>{#if source !== execution.entry.sql}<p>These results belong to the previously executed script.</p>{/if}{#if execution.entry.error}<p class="script-error" role="alert">{execution.entry.error}</p>{/if}{#if execution.rolledBack}<p>Transaction rolled back.</p>{/if}
    {#each execution.results as result}<article><h3>Statement {result.statement}</h3>{#if result.columns.length}<div class="script-grid"><table><thead><tr>{#each result.columns as column}<th>{column}</th>{/each}</tr></thead><tbody>{#each result.rows as row}<tr>{#each row as value}<td class:null-value={value === null} title={cell(value)}>{cell(value)}</td>{/each}</tr>{/each}</tbody></table></div><p>{result.rows.length} row(s){result.truncated ? ' · Result truncated; narrow the query or increase the row limit.' : ''}</p>{:else}<p>{result.rowsAffected} row(s) affected</p>{/if}</article>{/each}
  </section>{/if}
</section>

<style>
.sql-console{height:100%;overflow:auto;padding:22px;display:flex;flex-direction:column;gap:14px;min-width:0;color:var(--text,#dce0e5)}
header,header>div,.script-toolbar,article>div{display:flex;align-items:center;gap:12px}header{justify-content:space-between}header>div{align-items:baseline;flex-wrap:wrap}h1{font-size:19px;margin:0}h2{font-size:15px;margin:0 0 12px}h3{font-size:14px}header span,p,.script-toolbar>span{font-size:12px;color:var(--muted,#9aa2ad)}.sql-editor{min-height:200px;height:250px;flex-shrink:0;border:1px solid #ffffff20;border-radius:7px;overflow:hidden}.script-toolbar{flex-wrap:wrap}.script-toolbar>span{margin-right:auto}.script-toolbar label{font-size:12px}.script-toolbar input{width:72px;background:#ffffff06;border:1px solid #ffffff25;color:inherit;height:var(--control-height);padding:0 8px;border-radius:var(--control-radius);margin-left:8px}.script-results{border:1px solid #ffffff18;border-radius:7px;padding:15px}.script-mode{display:flex;align-items:flex-start;gap:8px;font-size:13px}.mode-warning{color:#e9be74;background:#e9be7410;padding:12px;border:1px solid #e9be7440;border-radius:5px}.script-error{color:#f6a0a0}.script-grid{overflow:auto;max-height:420px}table{border-collapse:collapse;width:100%;font-size:13px;text-align:left}th,td{padding:9px 12px;border-bottom:1px solid #ffffff12;max-width:500px;overflow:hidden;text-overflow:ellipsis;white-space:pre}th{background:#ffffff05;position:sticky;top:0}.null-value{color:#999;font-style:italic}input:focus-visible{outline:2px solid #83c96f;outline-offset:2px}
</style>
