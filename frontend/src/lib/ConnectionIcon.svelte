<script lang="ts">
  import { Database } from '@lucide/svelte';
  import type { components } from './api/schema';

  type Connection = components['schemas']['Connection'];
  let { driver, engine }: { driver: Connection['driver']; engine?: Connection['engine'] } = $props();

  const drivers = {
    mysql: { label: 'MySQL', color: '#f2bd76' },
    mariadb: { label: 'MariaDB', color: '#d2b49b' },
    postgres: { label: 'PostgreSQL', color: '#8ab8ed' },
    sqlite: { label: 'SQLite', color: '#8fd5ed' },
  };
  const product = $derived(engine ?? driver);
  const appearance = $derived(product === 'unknown' ? null : drivers[product]);
</script>

{#if appearance}
<span role="img" aria-label={appearance.label} title={appearance.label}
  style:background-color={appearance.color}
  style:mask-image={`url('/database-logos/${product}.svg')`}></span>
{:else}<Database size={26} aria-label="Unknown database driver" />{/if}

<style>
  span {
    display: inline-block;
    width: 26px;
    height: 26px;
    flex: 0 0 auto;
    mask-size: contain;
    mask-position: center;
    mask-repeat: no-repeat;
  }
</style>
