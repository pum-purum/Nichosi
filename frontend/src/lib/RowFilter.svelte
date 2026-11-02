<script lang="ts">
  import { Plus, RefreshCw, SlidersHorizontal, X } from '@lucide/svelte';
  import { filterOperators, formatRowFilter, parseRowFilter, type RowFilter } from './row-filter';
  let { columns, busy, apply, refresh, text = $bindable(''), appliedText = $bindable('') }: { columns: string[]; busy: boolean; apply: (filters: RowFilter[]) => void; refresh: () => void; text?: string; appliedText?: string } = $props();
  let expanded = $state(false);
  const parsed = $derived.by(() => {
    try { return { filters: parseRowFilter(text, columns), error: '' }; }
    catch (cause) { return { filters: [] as RowFilter[], error: (cause as Error).message }; }
  });
  function update(filters: RowFilter[]) { text = formatRowFilter(filters); }
  function change(index: number, patch: Partial<RowFilter>) { update(parsed.filters.map((filter, i) => i === index ? { ...filter, ...patch } : filter)); }
  function add(connector: 'and' | 'or') {
    if (parsed.error || !columns.length || parsed.filters.length >= 100) return;
    update([...parsed.filters, { column: columns[0], op: 'eq', value: '', connector }]); expanded = true;
  }
  function submit() { if (parsed.error || busy) return; appliedText = text; apply(parsed.filters); }
  function clear() { text = ''; appliedText = ''; apply([]); }
  function setOperator(index: number, op: RowFilter['op']) {
    const old = parsed.filters[index].value;
    change(index, { op, value: op === 'between' ? ['', ''] : op === 'in' ? [''] : Array.isArray(old) ? old[0] ?? '' : old ?? '' });
  }
  function setArrayValue(index: number, position: number, value: string) {
    const values = [...parsed.filters[index].value as string[]]; values[position] = value; change(index, { value: values });
  }
</script>

<form class="row-filter" onsubmit={(event) => { event.preventDefault(); submit(); }}>
  <div class="row-filter-bar">
    <button type="button" aria-label="Refresh data" title="Refresh data" disabled={busy} onclick={refresh}><RefreshCw size={15} /><span>Refresh</span></button>
    <button type="button" class:active={expanded} aria-expanded={expanded} title="Show filter builder" onclick={() => expanded = !expanded}><SlidersHorizontal size={15} /><span>Filter</span></button>
    <input class="filter-expression" aria-label="Row filter expression" aria-invalid={!!parsed.error} aria-describedby="row-filter-help row-filter-error" placeholder={'Choose conditions or type: `id` >= 10 OR `name` LIKE "Ann%"'} bind:value={text} spellcheck="false" autocomplete="off" />
    <button type="submit" class="filter-apply" disabled={busy || !!parsed.error || !columns.length}>Apply{text !== appliedText ? ' •' : ''}</button>
    <button type="button" aria-label="Clear row filter" title="Clear filter" disabled={busy || (!text && !appliedText)} onclick={clear}><X size={15} /></button>
  </div>
  {#if expanded}
    <div class="filter-builder">
      {#each parsed.filters as filter, index}
        <div class="filter-condition">
          {#if index > 0}<select aria-label={`Join condition ${index + 1}`} value={filter.connector ?? 'and'} onchange={(event) => change(index, { connector: event.currentTarget.value as 'and' | 'or' })}><option value="and">AND</option><option value="or">OR</option></select>{:else}<span class="filter-where">WHERE</span>{/if}
          <select class="filter-column" aria-label={`Column for condition ${index + 1}`} value={filter.column} onchange={(event) => change(index, { column: event.currentTarget.value })}>{#each columns as column}<option value={column}>{column}</option>{/each}</select>
          <select aria-label={`Operator for condition ${index + 1}`} value={filter.op} onchange={(event) => setOperator(index, event.currentTarget.value as RowFilter['op'])}>{#each filterOperators as operator}<option value={operator.op}>{operator.label}</option>{/each}</select>
          {#if filter.op === 'between' && Array.isArray(filter.value)}
            <input aria-label={`Lower bound for condition ${index + 1}`} placeholder="From" value={String(filter.value[0])} oninput={(event) => setArrayValue(index, 0, event.currentTarget.value)} /><span>AND</span><input aria-label={`Upper bound for condition ${index + 1}`} placeholder="To" value={String(filter.value[1])} oninput={(event) => setArrayValue(index, 1, event.currentTarget.value)} />
          {:else if filter.op === 'in' && Array.isArray(filter.value)}
            <div class="filter-in-values">{#each filter.value as value, valueIndex}<div><input aria-label={`Value ${valueIndex + 1} for condition ${index + 1}`} value={String(value)} oninput={(event) => setArrayValue(index, valueIndex, event.currentTarget.value)} /><button type="button" aria-label={`Remove value ${valueIndex + 1}`} disabled={filter.value.length === 1} onclick={() => change(index, { value: (filter.value as unknown[]).filter((_, i) => i !== valueIndex) })}><X size={13} /></button></div>{/each}<button type="button" disabled={filter.value.length >= 1000} onclick={() => change(index, { value: [...filter.value as unknown[], ''] })}><Plus size={13} />Value</button></div>
          {:else if filter.op !== 'isNull' && filter.op !== 'notNull'}<input aria-label={`Value for condition ${index + 1}`} placeholder={filter.op === 'like' ? 'Pattern, e.g. Ann%' : 'Value'} value={String(filter.value ?? '')} oninput={(event) => change(index, { value: event.currentTarget.value })} />{/if}
          <button type="button" aria-label={`Remove condition ${index + 1}`} title="Remove condition" onclick={() => update(parsed.filters.filter((_, i) => i !== index))}><X size={14} /></button>
        </div>
      {/each}
      <div class="filter-builder-actions"><button type="button" disabled={!!parsed.error || !columns.length || parsed.filters.length >= 100} onclick={() => add('and')}><Plus size={14} />{parsed.filters.length ? 'AND condition' : 'Add condition'}</button>{#if parsed.filters.length}<button type="button" disabled={!!parsed.error || parsed.filters.length >= 100} onclick={() => add('or')}><Plus size={14} />OR condition</button>{/if}</div>
    </div>
  {/if}
  {#if parsed.error}<p class="filter-error" id="row-filter-error" role="alert">{parsed.error}</p>{:else}<span id="row-filter-error"></span>{/if}
  {#if expanded || parsed.error}<p class="filter-help" id="row-filter-help">Click to build or edit the expression above. Enter applies. AND takes precedence over OR; grouping parentheses are unsupported. Use backticks for column names and quotes for text. LIKE uses % and _.</p>{:else}<span id="row-filter-help"></span>{/if}
</form>
