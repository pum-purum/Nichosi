<script lang="ts">
  let { id, name, options, value = $bindable(''), disabled = false, invalid = false, describedby }: {
    id: string; name: string; options: { kind: 'enum' | 'set'; values: string[] };
    value: string; disabled?: boolean; invalid?: boolean; describedby?: string;
  } = $props();
  const selected = $derived(value === '' ? [] : value.split(','));
  function toggle(member: string, checked: boolean) {
    value = options.values.filter(option => option === member ? checked : selected.includes(option)).join(',');
  }
</script>

{#if options.kind === 'enum'}
  <select {id} bind:value {disabled} aria-label={`${name} value`} aria-invalid={invalid} aria-describedby={describedby}>
    {#if !options.values.includes(value)}<option value={value} disabled>{value || 'Select a value'}</option>{/if}
    {#each options.values as member}<option value={member}>{member || '(empty string)'}</option>{/each}
  </select>
{:else}
  <details class:disabled class:invalid>
    <summary {id} aria-label={`${name} value`} aria-disabled={disabled} aria-describedby={describedby} onclick={(event) => { if (disabled) event.preventDefault(); }}>{value || 'No values selected'}</summary>
    <div class="members" role="group" aria-label={`${name} options`}>
      {#each options.values as member}
        <label><input type="checkbox" {disabled} checked={selected.includes(member)} onchange={(event) => toggle(member, event.currentTarget.checked)} />{member || '(empty string)'}</label>
      {/each}
    </div>
  </details>
{/if}

<style>
  select, summary { width: 100%; min-height: 37px; padding: 8px 10px; border: 1px solid var(--line); border-radius: 5px; background: var(--bg); color: var(--text); font: 13px/1.5 var(--mono); }
  summary { cursor: pointer; overflow-wrap: anywhere; }
  select:disabled, .disabled { opacity: .5; }
  .disabled summary { cursor: default; }
  [aria-invalid="true"], .invalid summary { border-color: #f39b9b; }
  select:focus-visible, summary:focus-visible, input:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  details { position: relative; }
  .members { position: absolute; z-index: 5; top: 100%; left: 0; width: 100%; max-height: 240px; overflow: auto; padding: 6px; border: 1px solid var(--line); border-radius: 5px; background: var(--surface-raised); box-shadow: 0 8px 20px #0006; }
  label { display: flex; align-items: center; gap: 8px; padding: 6px; overflow-wrap: anywhere; font: 13px/1.5 var(--mono); }
  input { accent-color: var(--accent); }
</style>
