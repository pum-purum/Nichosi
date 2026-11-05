<script lang="ts">
  import { onMount } from 'svelte';
  import { browserTimeZoneLabel, formatDateTime } from './date-time';
  import { RefreshCw, Trash2 } from '@lucide/svelte';
  import type { ScriptHistory } from './script-history';
  import type { components } from './api/schema';
  let { history, select, ready }: { history: ScriptHistory; select: (entry: components['schemas']['ScriptHistoryEntry']) => void; ready: boolean } = $props();
  onMount(() => { void history.load(); return () => history.dispose(); });
</script>

<div class="history-sidebar">
  <div class="history-heading"><div class="object-list-heading"><strong>Script history</strong></div>
  <div class="history-actions"><button disabled={$history.loading || $history.executing} onclick={() => void history.load()} aria-label="Refresh script history" title="Refresh history"><RefreshCw size={14} /></button><button disabled={$history.loading || $history.executing || !$history.entries.length} onclick={() => void history.clear()} aria-label="Clear script history" title="Clear history"><Trash2 size={14} /></button></div></div>
  {#if $history.error}<p class="history-error" role="alert">{$history.error}</p>{/if}
  {#if $history.loading}<p class="history-note" role="status">Loading history…</p>{/if}
  <nav class="history-list" aria-label="Script history">
    {#each $history.entries as entry (entry.id)}
      <button class:active={$history.selectedId === entry.id} class:succeeded={entry.success} class:failed={!entry.success} disabled={!ready || $history.executing} aria-current={$history.selectedId === entry.id ? 'true' : undefined} aria-label={`${entry.success ? 'Succeeded' : 'Failed'}. Load script to repeat: ${entry.sql.trim().replace(/\s+/g, ' ').slice(0, 160)}`} title={`${entry.success ? 'Succeeded' : 'Failed'}\n${entry.sql}\n${entry.error || (entry.atomic ? 'Atomic execution' : 'Partial execution allowed')}`} onclick={() => { history.select(entry.id); select(entry); }}>
        <span class="history-meta"><span class="history-source">{entry.sql.trim().replace(/\s+/g, ' ')}</span><small>{entry.durationMs} ms</small></span>
        <time datetime={entry.executedAt}>{formatDateTime(entry.executedAt)} <span class="timezone-badge">{browserTimeZoneLabel(entry.executedAt)}</span></time>
      </button>
    {:else}
      {#if !$history.loading && !$history.error}<p class="history-note" role="status">No scripts executed yet.</p>{/if}
    {/each}
  </nav>
</div>

<style>
.history-sidebar{display:flex;flex-direction:column;min-height:0;flex:1;position:relative}.history-heading{display:flex;align-items:center;gap:8px;flex:none;margin:12px 3px 16px}.object-list-heading{flex:1;min-width:0;margin:0}.history-actions{display:flex;flex-shrink:0;gap:3px}.history-actions button{display:grid;place-items:center;background:transparent;border:0;color:var(--muted);padding:4px;cursor:pointer}.history-note,.history-error{font-size:11px;line-height:1.5;margin:6px 12px 12px;color:var(--muted)}.history-error{color:#f6a0a0}.history-list{overflow:auto;min-height:0;flex:1;padding:0 8px 12px;display:flex;flex-direction:column;gap:5px}.history-list>button{display:flex;flex-direction:column;gap:7px;flex-shrink:0;text-align:left;width:100%;padding:11px 10px;background:transparent;border:1px solid transparent;border-radius:6px;color:var(--text);cursor:pointer}.history-list>button.succeeded{background:color-mix(in srgb,var(--accent) 9%,transparent);border-color:color-mix(in srgb,var(--accent) 28%,transparent)}.history-list>button.failed{background:#f6a0a012;border-color:#f6a0a055;border-style:dashed}.history-list>button:hover:not(:disabled){filter:brightness(1.15)}.history-list>button.active{box-shadow:inset 3px 0 var(--text)}.history-meta{display:flex;gap:8px;align-items:center;min-width:0;width:100%;font-size:11px}.history-meta small{flex-shrink:0;margin-left:auto;color:var(--muted)}.history-source{min-width:0;flex:1;font:12px/1.5 ui-monospace,monospace;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;width:100%}time{font-size:10px;color:var(--muted)}button:disabled{opacity:.45;cursor:default}button:focus-visible{outline:2px solid var(--accent);outline-offset:-2px}
</style>
