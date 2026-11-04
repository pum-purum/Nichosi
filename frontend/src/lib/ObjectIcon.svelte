<script lang="ts">
  import { Braces, Clock, Code2, Database, Eye, FolderTree, Hash, Layers, ListTree, Package, Shapes, ShieldCheck, TableProperties, UserRound, Zap } from '@lucide/svelte';
  import type { ObjectType } from './object-sections';
  let { type, size = 19 }: { type: ObjectType; size?: number } = $props();
  const icons = { table: TableProperties, view: Eye, materialized_view: Layers, trigger: Zap,
    role: ShieldCheck, user: UserRound, procedure: Braces, function: Code2, event: Clock,
    schema: FolderTree, sequence: Hash, type: Shapes, extension: Package, index: ListTree, cron: Clock };
  const Icon = $derived(icons[type] ?? Database);
</script>
<Icon {size} />
