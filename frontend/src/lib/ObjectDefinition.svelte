<script lang="ts">
  import { Copy, Check } from '@lucide/svelte';
  import { compactDefinition, formatDefinition, simplifyViewDefinition } from './sql-definition';
  import { highlightSQL } from './sql-highlight';

  let { definition, name, sqlMode, isView = false, preserveSource = false }: { definition: string; name: string; sqlMode?: string; isView?: boolean; preserveSource?: boolean } = $props();
  const script = $derived(!preserveSource && isView ? simplifyViewDefinition(definition) : definition);
  const formatted = $derived(preserveSource ? { sql: script, formatted: true } : formatDefinition(script, sqlMode));
  const displayedSQL = $derived(formatted.sql);
  const highlightedSQL = $derived(highlightSQL(displayedSQL));
  let copied = $state(false);
  let copyError = $state('');
  let copyAttempt = 0;
  $effect(() => { displayedSQL; copied = false; copyError = ''; copyAttempt++; });

  async function copyDefinition() {
    const attempt = ++copyAttempt;
    try {
      await navigator.clipboard.writeText(preserveSource ? script : compactDefinition(script, sqlMode));
      if (attempt === copyAttempt) { copied = true; copyError = ''; }
    } catch {
      if (attempt === copyAttempt) copyError = 'Could not copy SQL. Select the code and copy it manually.';
    }
  }
</script>

<div class="definition-panel">
  <div class="definition-toolbar">
    <h2>SQL definition</h2>
    <span>Read-only</span>
  </div>
  <div class="definition-code">
    <button class="copy-sql" onclick={copyDefinition} aria-label={`Copy SQL definition of ${name}`} title={copied ? 'Copied' : 'Copy SQL'}>
      {#if copied}<Check size={16} />{:else}<Copy size={16} />{/if}
    </button>
    <div class="definition-lines" aria-hidden="true">{displayedSQL.split('\n').map((_, index) => index + 1).join('\n')}</div>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (Scrollable SQL needs keyboard access.) -->
    <pre role="region" tabindex="0" aria-label={`SQL definition of ${name}`}><code class="language-sql">{@html highlightedSQL}</code></pre>
  </div>
  {#if !formatted.formatted}
    <p class="definition-feedback">Formatting is unavailable for this syntax. Showing the original SQL.</p>
  {/if}
  <p class="definition-feedback" class:copy-status={!copyError} role="status">{copyError || (copied ? 'SQL copied to clipboard.' : '')}</p>
</div>

<style>
  .definition-panel { margin-top: 28px; min-width: 0; }
  .definition-toolbar { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; padding: 10px 12px; background: var(--surface-raised); border: 1px solid var(--line); border-radius: 6px 6px 0 0; }
  .definition-toolbar h2 { margin: 0; font-size: 14px; }
  .definition-toolbar > span { color: #a8aeaa; font-size: 11px; }
  .copy-sql { position: absolute; top: 8px; right: 8px; z-index: 1; display: grid; place-items: center; width: 30px; height: 30px; padding: 0; border: 1px solid var(--line); border-radius: 5px; background: var(--surface-raised); color: var(--text); cursor: pointer; opacity: 0; pointer-events: none; }
  .definition-code:hover .copy-sql, .definition-code:focus-within .copy-sql { opacity: 1; pointer-events: auto; }
  .copy-sql:hover { background: var(--surface); }
  @media (hover: none) { .copy-sql { opacity: 1; pointer-events: auto; } }
  button:focus-visible, pre:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .definition-code { position: relative; display: flex; overflow: auto; background: var(--surface); border: 1px solid var(--line); border-top: 0; border-radius: 0 0 6px 6px; }
  .definition-lines, pre { margin: 0; padding: 16px; font: 13px/1.7 var(--mono); white-space: pre; tab-size: 4; }
  .definition-lines { flex: none; color: #8e9892; text-align: right; border-right: 1px solid var(--line-soft); user-select: none; }
  pre { padding-right: 52px; flex: 1; min-width: 0; overflow: auto; color: var(--sql-text); }
  code { font: inherit; }
  .definition-feedback { margin: 8px 0 0; color: #c6cdc8; font-size: 12px; }
  .copy-status { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  .definition-feedback:empty { display: none; }
</style>
