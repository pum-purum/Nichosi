<script lang="ts">
  import { Terminal, ChevronDown, ChevronRight, FolderTree, Settings2, ArrowLeft, Clock, ShieldCheck, UserRound, Database, HardDrive, KeyRound, Pencil, Plug, Search, Server, TableProperties, Trash2, Unplug, X } from '@lucide/svelte';
  import { onMount } from 'svelte';
  import { browserTimeZoneLabel, formatDateTime } from './lib/date-time';
  import ScriptHistorySidebar from './lib/ScriptHistorySidebar.svelte';
  import { createScriptHistory } from './lib/script-history';
  import DeleteRecordDialog from './lib/DeleteRecordDialog.svelte';
  import RecordEditor from './lib/RecordEditor.svelte';
  import TableMetadata from './lib/TableMetadata.svelte';
  import CellValue from './lib/CellValue.svelte';
  import ObjectDefinition from './lib/ObjectDefinition.svelte';
  import UserDetails from './lib/UserDetails.svelte';
  import DataError from './lib/DataError.svelte';
  import RowFilter from './lib/RowFilter.svelte';
  import ObjectIcon from './lib/ObjectIcon.svelte';
  import ConnectionIcon from './lib/ConnectionIcon.svelte';
  import { isViewType, objectSectionsFor, objectSectionType, postgresAccountBadge, type ObjectType } from './lib/object-sections';
  import { groupSchemaEntries, matchesSchemaEntry, schemaGroupedTypes, schemaGroupKey, schemaGroupOpen } from './lib/schema-groups';
  import { matchesObjectFilter } from './lib/object-filter';
  import { recoveryConnection } from './lib/connection-recovery';
  import type { components } from './lib/api/schema';

  type Connection = components['schemas']['Connection'];
  type TableInfo = components['schemas']['TableInfo'];
  type RowsPage = components['schemas']['RowsPage'];
  type DatabaseObject = components['schemas']['DatabaseObject'];
  let sqlConsoleOpen = $state(false);
  let sqlConsole = $state<{ repeat: (entry: components['schemas']['ScriptHistoryEntry']) => void }>();
  let supportedObjectTypes = $state<ObjectType[]>([]);

  let connections = $state<Connection[]>([]);
  let error = $state('');
  let loading = $state(true);
  let selectedConnection = $state<Connection | null>(null);
  const historyConnectionId = $derived(selectedConnection?.id ?? '');
  const scriptHistory = $derived(createScriptHistory(historyConnectionId));
  const objectSections = $derived(objectSectionsFor(supportedObjectTypes, selectedConnection?.driver));
  let selectedTable = $state('');
  let tableTab = $state<'data' | 'info'>('data');
  let activeObjectType = $state<ObjectType>('table');
  let selectedObject = $state<DatabaseObject | null>(null);
  const relationName = $derived(isViewType(activeObjectType) ? selectedObject?.name ?? '' : selectedTable);
  let definitionLoading = $state(false);
  let definitionError = $state('');
  let definitionRequestId = 0;
  let tableSearch = $state('');
  let tables = $state<TableInfo[]>([]);
  let metadataWarnings = $state<string[]>([]);
  let objects = $state<components['schemas']['DatabaseObject'][]>([]);
  let filterInput = $state<HTMLInputElement>();
  let collapsedSchemas = $state<Record<string, boolean>>({});
  let schemaProperties = $state<DatabaseObject | null>(null);
  let schemaPropertiesLoading = $state(false);
  let schemaPropertiesError = $state('');
  let schemaPropertiesRequestId = 0;
  const groupedSidebar = $derived(selectedConnection?.driver === 'postgres' && schemaGroupedTypes.includes(activeObjectType));
  const categoryObjects = $derived(objects.filter(object => objectSectionType(object.type, selectedConnection?.driver) === activeObjectType));
  const sidebarSchemaGroups = $derived(groupSchemaEntries<TableInfo | DatabaseObject>(activeObjectType === 'table' ? tables : categoryObjects, objects, tableSearch));
  function closeSchemaProperties() {
    schemaPropertiesRequestId++;
    schemaProperties = null; schemaPropertiesLoading = false; schemaPropertiesError = '';
  }
  function toggleSchema(schemaName: string) {
    const key = schemaGroupKey(activeObjectType, schemaName);
    collapsedSchemas = { ...collapsedSchemas, [key]: !collapsedSchemas[key] };
  }
  async function openSchemaProperties(schema: DatabaseObject) {
    sqlConsoleOpen = false;
    const connectionId = selectedConnection?.id;
    if (!connectionId) return;
    editingRow = null;
    const requestId = ++schemaPropertiesRequestId;
    schemaProperties = schema; schemaPropertiesLoading = true; schemaPropertiesError = '';
    try {
      const query = new URLSearchParams({ type: 'schema', name: schema.name });
      const result = await request<DatabaseObject>(`/api/connections/${encodeURIComponent(connectionId)}/object-definition?${query}`);
      if (requestId === schemaPropertiesRequestId && selectedConnection?.id === connectionId) schemaProperties = { ...schema, ...result, details: { ...schema.details, ...result.details } };
    } catch (cause) {
      if (requestId === schemaPropertiesRequestId && selectedConnection?.id === connectionId) schemaPropertiesError = cause instanceof Error ? cause.message : 'Could not load schema properties';
    } finally {
      if (requestId === schemaPropertiesRequestId) schemaPropertiesLoading = false;
    }
  }
  const filteredTables = $derived(tables.filter(table => matchesSchemaEntry(tableSearch, table)));
  const filteredObjects = $derived(objects.filter(object => objectSectionType(object.type, selectedConnection?.driver) === activeObjectType && matchesObjectFilter(tableSearch, objectDisplayName(object), object.name, object.schemaName ?? '')));
  const filteredCount = $derived(activeObjectType === 'table' ? filteredTables.length : filteredObjects.length);
  const sectionLabel = $derived(objectSections.find(section => section.type === activeObjectType)?.label ?? 'Objects');
  function clearObjectFilter() { tableSearch = ''; filterInput?.focus(); }
  let largestTableBytes = $derived(tables.reduce((largest, table) => Math.max(largest, table.dataBytes + table.indexBytes), 0));
  let rowsPage = $state<RowsPage | null>(null);
  let editingRow = $state<unknown[] | null>(null);
  let deletingRow = $state<unknown[] | null>(null);
  function rowActionTooltip(row: unknown[]): string {
    const page = rowsPage;
    if (!page) return '';
    const primaryKey = page.columns.filter(column => column.primaryKey).map(column => column.name);
    const keyNames = primaryKey.length ? primaryKey : page.editKey ?? [];
    return keyNames.map(name => {
      const value = row[page.columns.findIndex(column => column.name === name)];
      return `${name}=${typeof value === 'string' ? JSON.stringify(value) : String(value)}`;
    }).join(', ');
  }
  function deleteRow(row: unknown[]) {
    if (rowActionDisabledReason || rowsLoading || databaseLoading || !rowsPage?.editKey?.length) return;
    deletingRow = structuredClone($state.snapshot(row));
  }
  function editRow(row: unknown[]) {
    if (rowActionDisabledReason || rowsLoading || databaseLoading || !rowsPage?.editKey?.length) return;
    editingRow = [...row];
  }
  async function recordSaved() {
    editingRow = null;
    await selectTable(relationName, currentPage);
  }
  const rowActionDisabledReason = $derived(isViewType(activeObjectType) ? 'Views are read-only' : selectedConnection?.readonly !== false ? 'Read-only connection' : !rowsPage?.editable ? rowsPage?.readOnlyReason || 'Read-only table' : '');
  let rowFilters = $state<components['schemas']['Filter'][]>([]);
  let filterColumns = $state<string[]>([]);
  let filterVersion = $state(0);
  let filterText = $state('');
  let appliedFilterText = $state('');
  function resetRowFilters() { filterText = ''; appliedFilterText = ''; rowFilters = []; filterColumns = []; filterVersion++; }
  function applyRowFilters(filters: components['schemas']['Filter'][]) { rowFilters = filters; void selectTable(relationName, 1); }
  let orderBy = $state<components['schemas']['OrderBy'][]>([]);
  let currentPage = $state(1);
  let pageSize = $state(50);
  let databaseLoading = $state(false);
  let rowsLoading = $state(false);
  let pendingPageControl = $state<number | 'previous' | 'next' | null>(null);
  let rowsRequestId = 0;
  let databaseRequestId = 0;
  let reconnecting = $state(false);
  let totalPages = $derived(Math.max(1, Math.ceil((rowsPage?.totalRows ?? 0) / pageSize)));
  let pagedRows = $derived(rowsPage?.rows ?? []);
  let pageItems = $derived.by(() => {
    if (totalPages <= 5) return Array.from({ length: totalPages }, (_, index) => index + 1);
    const middle = Math.ceil(totalPages / 2);
    const pages = new Set<number>();
    const add = (page: number) => {
      if (pages.size < 5 && page >= 1 && page <= totalPages) pages.add(page);
    };

    add(currentPage);
    add(currentPage - 1);
    add(currentPage + 1);
    add(1);
    add(totalPages);
    if (currentPage <= 3 || currentPage >= totalPages - 2) add(middle);
    add(2);
    add(totalPages - 1);
    add(middle);
    return [...pages].sort((a, b) => a - b);
  });


  async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const response = await fetch(path, {
      ...init,
      headers: { 'Content-Type': 'application/json', ...init.headers },
    });
    if (!response.ok) {
      const body = await response.json().catch(() => null);
      throw new Error(body?.message ?? `Request failed (${response.status})`);
    }
    return response.json() as Promise<T>;
  }

  async function loadConnections() {
    loading = true;
    error = '';
    try {
      connections = await request<Connection[]>('/api/connections');
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Could not load connections';
    } finally { loading = false; }
  }

  async function openDatabase(connection: Connection) {
    closeSchemaProperties(); collapsedSchemas = {};
    editingRow = null;
    const requestId = ++databaseRequestId;
    rowsRequestId += 1;
    rowsLoading = false;
    pendingPageControl = null;
    orderBy = []; resetRowFilters();
    selectedConnection = connection; tableSearch = ''; tableTab = 'data'; selectedTable = ''; activeObjectType = 'table'; selectedObject = null; definitionRequestId++; definitionLoading = false; definitionError = ''; rowsPage = null; tables = []; objects = []; supportedObjectTypes = []; metadataWarnings = []; error = ''; databaseLoading = true;
    try {
      const connected = await request<Connection>(`/api/connections/${encodeURIComponent(connection.id)}/connect`, { method: 'POST' });
      if (requestId !== databaseRequestId) return;
      selectedConnection = connected;
      connections = connections.map((item) => item.id === connected.id ? connected : item);
      const schema = await request<components['schemas']['Schema']>(`/api/connections/${encodeURIComponent(connection.id)}/schema`);
      if (requestId !== databaseRequestId) return;
      tables = schema.tables; objects = schema.objects; supportedObjectTypes = schema.supportedObjectTypes; metadataWarnings = schema.warnings ?? [];
      if (activeObjectType === 'table') {
        if (tables.length) await selectTable(tables[0].name, 1, null, 'table');
      } else selectSection(activeObjectType);
    } catch (cause) { if (requestId === databaseRequestId) error = cause instanceof Error ? cause.message : 'Could not read database schema'; }
    finally { if (requestId === databaseRequestId) databaseLoading = false; }
  }

  async function selectTable(name: string, page = 1, control: number | 'previous' | 'next' | null = null, kind: 'table' | 'view' | 'materialized_view' = isViewType(activeObjectType) ? activeObjectType : 'table') {
    sqlConsoleOpen = false;
    if (!selectedConnection || !name) return;
    closeSchemaProperties();
    editingRow = null;
    const requestId = ++rowsRequestId;
    const minimumLoadingTime = new Promise<void>((resolve) => setTimeout(resolve, 200));
    const changingRelation = activeObjectType !== kind || relationName !== name;
    activeObjectType = kind;
    if (kind === 'table') { selectedObject = null; selectedTable = name; }
    definitionRequestId++; definitionLoading = false; definitionError = '';
    if (changingRelation) { resetRowFilters(); orderBy = []; rowsPage = null; currentPage = 1; }
    error = ''; rowsLoading = true; pendingPageControl = control;
    try {
      const result = await request<RowsPage>(`/api/connections/${encodeURIComponent(selectedConnection.id)}/${isViewType(kind) ? 'views' : 'tables'}/${encodeURIComponent(name)}/rows`, { method: 'POST', body: JSON.stringify({ limit: pageSize, offset: (page - 1) * pageSize, orderBy, filters: rowFilters }) });
      await minimumLoadingTime;
      if (requestId === rowsRequestId) { rowsPage = result; filterColumns = result.columns.map(column => column.name); currentPage = page; }
    } catch (cause) {
      await minimumLoadingTime;
      if (requestId === rowsRequestId) { error = cause instanceof Error ? cause.message : 'Could not read rows'; rowsPage = null; }
    } finally {
      if (requestId === rowsRequestId) { rowsLoading = false; pendingPageControl = null; }
    }
  }

  function sortColumn(column: string) {
    if (rowsLoading || databaseLoading) return;
    orderBy = [{ column, direction: orderBy[0]?.column === column && orderBy[0].direction === 'asc' ? 'desc' : 'asc' }];
    void selectTable(relationName, 1);
  }

  async function reconnectDatabase() {
    const previous = selectedConnection;
    if (!previous || reconnecting) return;
    const requestId = databaseRequestId;
    reconnecting = true;
    try {
      const current = await request<Connection[]>('/api/connections');
      if (selectedConnection !== previous || requestId !== databaseRequestId) return;
      connections = current;
      const refreshed = recoveryConnection(previous, current);
      if (!refreshed) {
        error = 'This connection has changed or is no longer configured. Return to Connections and select a profile.';
        return;
      }
      await openDatabase(refreshed);
    } catch (cause) {
      if (selectedConnection === previous && requestId === databaseRequestId) error = cause instanceof Error ? cause.message : 'Could not reconnect';
    } finally { reconnecting = false; }
  }

  function showConnections() {
    closeSchemaProperties(); collapsedSchemas = {};
    editingRow = null;
    databaseRequestId++;
    rowsRequestId++;
    definitionRequestId++;
    resetRowFilters();
    selectedConnection = null;
    selectedObject = null;
    rowsPage = null;
    databaseLoading = false;
    rowsLoading = false;
    definitionLoading = false;
    pendingPageControl = null;
    void loadConnections();
  }

  function selectSection(type: ObjectType) {
    sqlConsoleOpen = false;
    closeSchemaProperties();
    editingRow = null;
    resetRowFilters();
    orderBy = []; rowsRequestId++; rowsLoading = false; pendingPageControl = null; rowsPage = null; currentPage = 1; error = '';
    activeObjectType = type;
    tableTab = 'data'; tableSearch = '';
    if (type === 'table') {
      selectedObject = null;
      definitionRequestId++; definitionLoading = false; definitionError = '';
      const name = selectedTable || tables[0]?.name;
      if (name) void selectTable(name, 1, null, 'table');
      return;
    }
    const object = objects.find((object) => objectSectionType(object.type, selectedConnection?.driver) === type) ?? null;
    if (object) void selectObject(object);
    else { selectedObject = null; definitionRequestId++; definitionLoading = false; definitionError = ''; }
  }

  async function selectObject(object: DatabaseObject) {
    sqlConsoleOpen = false;
    closeSchemaProperties();
    editingRow = null;
    const changed = selectedObject?.name !== object.name || activeObjectType !== objectSectionType(object.type, selectedConnection?.driver);
    selectedObject = object;
    activeObjectType = objectSectionType(object.type, selectedConnection?.driver);
    if (isViewType(object.type)) {
      if (changed) { resetRowFilters(); orderBy = []; rowsPage = null; currentPage = 1; }
      await selectTable(object.name, 1, null, object.type);
      return;
    }
    const connectionId = selectedConnection?.id;
    const requestId = ++definitionRequestId;
    definitionError = '';
    definitionLoading = false;
    if (!connectionId || object.type === 'role' || object.type === 'user') return;
    definitionLoading = true;
    try {
      const query = new URLSearchParams({ type: object.type, name: object.name });
      const result = await request<DatabaseObject>(`/api/connections/${encodeURIComponent(connectionId)}/object-definition?${query}`);
      if (requestId === definitionRequestId && selectedObject === object && selectedConnection?.id === connectionId) {
        selectedObject = { ...object, definition: result.definition, details: { ...object.details, ...result.details } };
      }
    } catch (cause) {
      if (requestId === definitionRequestId && selectedObject === object && selectedConnection?.id === connectionId) {
        definitionError = cause instanceof Error ? cause.message : 'Could not load SQL definition';
      }
    } finally {
      if (requestId === definitionRequestId) definitionLoading = false;
    }
  }

  function objectDisplayName(object: DatabaseObject): string {
    if (object.type === 'user' || object.type === 'role') {
      const account = object.details?.['Account name'];
      const host = object.details?.['Host'];
      if (account !== undefined && host !== undefined) return `${account}@${host}`;
    }
    return object.displayName ?? object.name;
  }

  function accountBadge(object: DatabaseObject) {
    if (selectedConnection?.driver === 'postgres') return postgresAccountBadge(object);
    if (selectedConnection?.driver !== 'mysql' || (object.type !== 'user' && object.type !== 'role')) return null;
    const category = object.details?.['Account category'];
    if (category === 'internal') return { label: 'Built-in', title: 'Recognized internal MySQL account identity; this does not indicate SYSTEM_USER privileges.' };
    if (category === 'default-admin') return { label: 'Default admin', title: 'Standard initial administrator identity; current privileges and creation history are not verified.' };
    return null;
  }

  function sectionCount(type: ObjectType) {
    return type === 'table' ? tables.length : objects.filter((object) => objectSectionType(object.type, selectedConnection?.driver) === type).length;
  }

  async function disconnect() {
    editingRow = null;
    const connection = selectedConnection;
    if (!connection) return;
    const requestId = ++databaseRequestId;
    rowsRequestId++;
    definitionRequestId++;
    databaseLoading = false;
    rowsLoading = false;
    definitionLoading = false;
    pendingPageControl = null;
    try {
      const closed = await request<Connection>(`/api/connections/${encodeURIComponent(connection.id)}/disconnect`, { method: 'POST' });
      if (requestId !== databaseRequestId) return;
      connections = connections.map((item) => item.id === closed.id ? closed : item);
      showConnections();
    } catch (cause) {
      if (requestId === databaseRequestId) error = cause instanceof Error ? cause.message : 'Could not disconnect from the database';
    }
  }

  onMount(() => { void loadConnections(); });

  function formatBytes(bytes: number | null) {
    if (bytes === null) return 'Unavailable';
    if (bytes < 1024) return `${bytes} B`;
    const units = ['KB', 'MB', 'GB', 'TB'];
    let size = bytes / 1024;
    let unit = 0;
    while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit += 1; }
    return `${size.toFixed(1)} ${units[unit]}`;
  }


</script>

{#snippet connectionInstructions()}
  <div class="config-instructions">
    <p class="config-source">All database connections are loaded from <code>data/config.json</code>.</p>
    <ol class="config-steps">
      <li><span>01</span><p>Add a connection object, choose its driver and fill in the database details. For SQLite, use an absolute file path in database.</p></li>
      <li><span>02</span><p>For server databases, set <code>passwordEnv</code> to the password environment variable. SQLite needs no password.</p></li>
      <li><span>03</span><p>For SQLite, mount the existing file into the backend container. Save the profile, then restart Nichosi.</p></li>
    </ol>
    <pre class="config-example"><code>{`{
  "name": "My database",
  "driver": "mysql",
  "host": "mysql",
  "port": 3306,
  "user": "database_user",
  "database": "database_name",
  "readonly": false,
  "passwordEnv": "NICHOSI_DATABASE_PASSWORD"
}`}</code></pre>
    <p class="config-note">SQLite example: <code>{'{"name":"SQLite database","driver":"sqlite","database":"/sqlite/database.sqlite","readonly":false}'}</code>. Host, port and user are not used in saved SQLite profiles.</p>
  </div>
{/snippet}

{#snippet sidebarTable(table: TableInfo)}
                  <button class:active={selectedTable === table.name} class="table-nav-item table-with-size" title={table.name} aria-current={selectedTable === table.name ? 'page' : undefined} onclick={() => void selectTable(table.name)}>
                    <TableProperties size={17} /><span>{table.displayName ?? table.name}</span><small class="table-size" title={`${formatBytes(table.dataBytes + table.indexBytes)} · ${largestTableBytes > 0 ? ((table.dataBytes + table.indexBytes) / largestTableBytes * 100).toFixed(1) : '0'}% of the largest table`}>
                      <span>{formatBytes(table.dataBytes + table.indexBytes)}</span>
                      <span class="table-size-track" aria-hidden="true"><span class="table-size-fill" style:width={`${largestTableBytes > 0 ? (table.dataBytes + table.indexBytes) / largestTableBytes * 100 : 0}%`}></span></span>
                    </small>
                  </button>
{/snippet}

{#snippet sidebarObject(object: DatabaseObject)}
                  {@const badge = accountBadge(object)}
                  <button class:active={selectedObject?.name === object.name} class="table-nav-item" title={object.name} aria-current={selectedObject?.name === object.name ? 'page' : undefined} onclick={() => selectObject(object)}>
                    <ObjectIcon type={activeObjectType} size={17} /><span>{objectDisplayName(object)}</span>
                    {#if badge}<small class="account-badge" title={badge.title}>{badge.label}</small>{/if}
                  </button>
{/snippet}

<svelte:head><title>{selectedConnection?.database || 'Databases'} · Nichosi</title></svelte:head>

<div class="app-shell">
  <main class:has-connections={connections.length > 0} class:database-open={selectedConnection !== null}>
    {#if selectedConnection}
      <section class="database-workspace" aria-label="Database workspace">
        <nav class="app-rail" aria-label="Workspace navigation">
          {#each objectSections as section}
            <button class:active={!sqlConsoleOpen && activeObjectType === section.type} class="rail-button category-rail-button" data-object-type={section.type} aria-label={section.label} aria-current={!sqlConsoleOpen && activeObjectType === section.type ? 'page' : undefined} title={section.label} onclick={() => selectSection(section.type)}>
              <ObjectIcon type={section.type} />
              {#if sectionCount(section.type) > 0}<small>{sectionCount(section.type) > 99 ? '99+' : sectionCount(section.type)}</small>{/if}
            </button>
          {/each}
          <button class="rail-button" class:active={sqlConsoleOpen} aria-label="SQL console" title="SQL console" aria-current={sqlConsoleOpen ? 'page' : undefined} onclick={() => sqlConsoleOpen = true}><Terminal size={20} /></button>
          <span class="rail-spacer"></span>
        </nav>
        <aside class="schema-sidebar">
          {#if sqlConsoleOpen}
            {#key selectedConnection.id}<ScriptHistorySidebar history={scriptHistory} ready={!!sqlConsole} select={entry => sqlConsole?.repeat(entry)} />{/key}
          {:else}
          <div class="tree-branch">
            <div class="object-list-heading"><strong>{sectionLabel}</strong></div>
            <div class="table-search"><Search size={14} /><input bind:this={filterInput} type="search" bind:value={tableSearch} placeholder={`Filter ${sectionLabel.toLowerCase()}`} aria-label="Filter database objects" aria-controls="database-object-list" autocomplete="off" spellcheck="false" onkeydown={(event) => { if (event.key === 'Escape') { event.preventDefault(); clearObjectFilter(); } }} />{#if tableSearch}<button class="clear-object-filter" aria-label="Clear object filter" title="Clear filter" onclick={clearObjectFilter}><X size={14} /></button>{/if}</div>
            <nav id="database-object-list" class="table-nav" aria-label={objectSections.find((section) => section.type === activeObjectType)?.label ?? 'Database objects'}>
              {#if groupedSidebar}
                {#each sidebarSchemaGroups as group, groupIndex (group.schemaName)}
                  {@const open = schemaGroupOpen(collapsedSchemas[schemaGroupKey(activeObjectType, group.schemaName)], tableSearch)}
                  <section class="schema-group" aria-label={group.schemaName || 'Unspecified schema'}>
                    <div class="schema-group-heading">
                      <button class="schema-group-toggle" title={tableSearch.trim() ? `${group.schemaName} · Expanded for search` : group.schemaName} disabled={!!tableSearch.trim()} aria-expanded={open} aria-controls={`schema-group-${groupIndex}`} onclick={() => toggleSchema(group.schemaName)}>
                        {#if open}<ChevronDown size={14} />{:else}<ChevronRight size={14} />{/if}<FolderTree size={15} /><span>{group.schemaName || 'Unspecified schema'}</span><small>{group.items.length}</small>
                      </button>
                      {#if group.schema}<button class="schema-properties-button" title={`Schema properties: ${group.schemaName}`} aria-label={`Schema properties: ${group.schemaName}`} onclick={() => group.schema && openSchemaProperties(group.schema)}><Settings2 size={14} /></button>{/if}
                    </div>
                    <div id={`schema-group-${groupIndex}`} class="schema-group-items" hidden={!open}>
                      {#each group.items as item (item.name)}
                        {#if 'dataBytes' in item}{@render sidebarTable(item)}{:else}{@render sidebarObject(item)}{/if}
                      {:else}<p class="schema-group-empty">No {sectionLabel.toLowerCase()}.</p>{/each}
                    </div>
                  </section>
                {/each}
              {:else if activeObjectType === 'table'}
                {#each filteredTables as table}{@render sidebarTable(table)}{/each}
              {:else}
                {#each filteredObjects as object}{@render sidebarObject(object)}{/each}
              {/if}
              {#if filteredCount === 0 && !databaseLoading && (!groupedSidebar || sidebarSchemaGroups.length === 0)}<p class="object-list-empty" role="status">{#if tableSearch.trim()}No matching {sectionLabel.toLowerCase()}.{:else}No {sectionLabel.toLowerCase()} found.{/if}</p>{/if}
            </nav>
          </div>
          {/if}
          <div class="sidebar-footer">
            <button class="connection-status" onclick={() => void disconnect()} aria-label={`Disconnect from ${selectedConnection.name}`} title="Disconnect">
              <span class="connection-dot"></span><span>Connected</span><span class="sidebar-footer-host">{selectedConnection.driver === 'sqlite' ? selectedConnection.database : `${selectedConnection.host}:${selectedConnection.port}`}</span><Unplug size={15} />
            </button>
          </div>
        </aside>

        <div class="workspace-main">
        {#if sqlConsoleOpen}
          {#await import('./lib/SqlConsole.svelte')}
            <p role="status">Loading SQL console…</p>
          {:then module}
            {@const SqlConsole = module.default}
            {#key selectedConnection.id}<SqlConsole connection={selectedConnection} history={scriptHistory} bind:this={sqlConsole} />{/key}
          {/await}
        {:else if schemaProperties}
          <section class="object-main" aria-label="Schema properties">
            <div class="object-detail-card">
              <div class="schema-properties-toolbar"><button class="schema-properties-back" onclick={closeSchemaProperties}><ArrowLeft size={15} />Back to {sectionLabel.toLowerCase()}</button><button class="schema-properties-close" aria-label="Close schema properties" title="Close schema properties" onclick={closeSchemaProperties}><X size={17} /></button></div>
              <div class="object-heading"><h1>{schemaProperties.displayName ?? schemaProperties.schemaName ?? schemaProperties.name}</h1><span>Schema properties</span></div>
              {#if schemaPropertiesLoading}<p role="status">Loading schema properties…</p>{:else if schemaPropertiesError}<p role="alert">{schemaPropertiesError}</p><button onclick={() => schemaProperties && openSchemaProperties(schemaProperties)}>Retry schema properties</button>{/if}
              {#if schemaProperties.details}<dl class="object-properties">{#each Object.entries(schemaProperties.details) as [label, value]}<div><dt>{label}</dt><dd>{value}</dd></div>{/each}</dl>{/if}
              {#if schemaProperties.definition}<ObjectDefinition preserveSource definition={schemaProperties.definition} name={schemaProperties.name} />{:else if !schemaPropertiesLoading && !schemaPropertiesError}<p>{schemaProperties.details?.['Definition status'] || 'SQL definition is unavailable to this account.'}</p>{/if}
            </div>
          </section>
        {:else if editingRow && rowsPage && selectedConnection}
          <RecordEditor connectionId={selectedConnection.id} table={selectedTable} columns={rowsPage.columns} row={editingRow} editKey={rowsPage.editKey ?? []} cancel={() => editingRow = null} saved={recordSaved} />
        {:else if activeObjectType === 'table' || isViewType(activeObjectType)}
          <div class="table-main">
            <div class="table-bottom-tabs" tabindex="-1" role="tablist" aria-label={isViewType(activeObjectType) ? 'View workspace' : 'Table workspace'} onkeydown={(event) => {
              if (!relationName) return;
              if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
              event.preventDefault();
              tableTab = event.key === 'Home' ? 'data' : event.key === 'End' ? 'info' : tableTab === 'data' ? 'info' : 'data';
              (event.currentTarget.querySelector(`[id="table-${tableTab}-tab"]`) as HTMLButtonElement)?.focus();
            }}>
              <button id="table-data-tab" role="tab" aria-selected={tableTab === 'data'} aria-controls="table-data-panel" tabindex={tableTab === 'data' ? 0 : -1} onclick={() => tableTab = 'data'}><TableProperties size={15} />Data</button>
              <button id="table-info-tab" role="tab" aria-selected={tableTab === 'info'} aria-controls="table-info-panel" tabindex={tableTab === 'info' ? 0 : -1} disabled={!relationName} onclick={() => tableTab = 'info'}><Database size={15} />{isViewType(activeObjectType) ? 'View info' : 'Table info'}</button>
            </div>
            <div id="table-data-panel" role="tabpanel" tabindex="0" aria-labelledby="table-data-tab" hidden={tableTab !== 'data'} class="table-data-panel">
            {#if relationName}{#key filterVersion}<RowFilter bind:text={filterText} bind:appliedText={appliedFilterText} columns={filterColumns} busy={rowsLoading || databaseLoading} apply={applyRowFilters} refresh={() => void selectTable(relationName, currentPage)} />{/key}{/if}
            <div class="data-grid-wrap" aria-busy={databaseLoading || rowsLoading}>{#if !databaseLoading && error}<DataError message={error} busy={reconnecting} reconnect={() => void reconnectDatabase()} back={showConnections} />{:else if rowsPage}<table class="data-grid" inert={databaseLoading || rowsLoading}><thead><tr>{#each rowsPage.columns as column}<th aria-sort={orderBy[0]?.column === column.name ? orderBy[0].direction === 'desc' ? 'descending' : 'ascending' : 'none'}><button class="column-sort" onclick={() => sortColumn(column.name)} title={'Sort by ' + column.name}><span class="column-name">{#if column.primaryKey}<KeyRound size={13} />{/if}{column.name}<span class="sort-arrows" aria-hidden="true"><span class="sort-chevron sort-chevron-up" class:active={orderBy[0]?.column === column.name && orderBy[0].direction === 'asc'}></span><span class="sort-chevron sort-chevron-down" class:active={orderBy[0]?.column === column.name && orderBy[0].direction === 'desc'}></span></span></span></button><small>{column.type}{#if column.primaryKey}<em>PK</em>{/if}</small></th>{/each}{#if activeObjectType === 'table'}<th class="actions-head">Actions</th>{/if}</tr></thead><tbody>{#each pagedRows as row}<tr>{#each row as value, columnIndex}<td><CellValue {value} sqlType={rowsPage.columns[columnIndex]?.type ?? ''} /></td>{/each}{#if activeObjectType === 'table'}<td class="row-actions"><button class="edit-row" onclick={() => editRow(row)} aria-label={`Edit row · ${rowActionTooltip(row)}`} disabled={!!rowActionDisabledReason} title={rowActionTooltip(row)}><Pencil size={16} /></button><button class="delete-row" onclick={() => deleteRow(row)} aria-label={`Delete row · ${rowActionTooltip(row)}`} disabled={!!rowActionDisabledReason} title={rowActionTooltip(row)}><Trash2 size={16} /></button></td>{/if}</tr>{/each}</tbody></table>{:else if !databaseLoading && !rowsLoading}<p class="grid-empty">{#if isViewType(activeObjectType)}Select a view from the left menu.{:else if tables.length === 0}No tables in this database.{:else}Select a table from the left menu.{/if}</p>{/if}{#if databaseLoading || (rowsLoading && pendingPageControl === null)}<div class="grid-loading" role="status"><span class="grid-spinner"></span><span>Loading data…</span></div>{/if}</div>
            {#if !error}<footer class="grid-footer"><span>Showing <strong>{rowsPage && rowsPage.totalRows > 0 ? `${(currentPage - 1) * pageSize + 1}–${Math.min(currentPage * pageSize, rowsPage.totalRows)} of ${rowsPage.totalRows}` : '0 of 0'} rows</strong></span><label class="page-size-label">Rows/page: <select aria-label="Rows per page" disabled={rowsLoading || databaseLoading || !relationName} bind:value={pageSize} onchange={() => void selectTable(relationName, 1)}><option value={25}>25</option><option value={50}>50</option><option value={100}>100</option></select></label><span class="grid-summary">Rows: <strong>{pagedRows.length}</strong> &nbsp; Columns: <strong>{rowsPage?.columns.length ?? 0}</strong></span><span class="pagination">{#if currentPage > 1}<button disabled={rowsLoading} onclick={() => void selectTable(relationName, currentPage - 1, 'previous')} aria-label="Previous page" aria-busy={pendingPageControl === 'previous'}>{#if pendingPageControl === 'previous'}<span class="page-spinner" aria-hidden="true"></span>{:else}‹{/if}</button>{/if}{#each pageItems as page, index}{#if index > 0 && page > pageItems[index - 1] + 1}<span class="pagination-ellipsis">…</span>{/if}{#if currentPage === page}<span class="active" aria-current="page" aria-label={`Page ${page}`}>{page}</span>{:else}<button disabled={rowsLoading} onclick={() => void selectTable(relationName, page, page)} aria-label={`Page ${page}`} aria-busy={pendingPageControl === page}>{#if pendingPageControl === page}<span class="page-spinner" aria-hidden="true"></span>{:else}{page}{/if}</button>{/if}{/each}{#if currentPage < totalPages}<button disabled={rowsLoading} onclick={() => void selectTable(relationName, currentPage + 1, 'next')} aria-label="Next page" aria-busy={pendingPageControl === 'next'}>{#if pendingPageControl === 'next'}<span class="page-spinner" aria-hidden="true"></span>{:else}›{/if}</button>{/if}</span></footer>{/if}
            </div>
            <div id="table-info-panel" role="tabpanel" tabindex="0" aria-labelledby="table-info-tab" class="table-info-panel" hidden={tableTab !== 'info'}>
              {#if tableTab === 'info' && relationName}
                {#key `${selectedConnection.id}/${activeObjectType}/${relationName}`}<TableMetadata materialized={activeObjectType === 'materialized_view'} preserveSource={selectedConnection.driver !== 'mysql'} connectionId={selectedConnection.id} table={relationName} relationType={isViewType(activeObjectType) ? 'view' : 'table'} />{/key}
              {/if}
            </div>

          </div>
        {:else}
          <section class="object-main" aria-label={`${objectSections.find((section) => section.type === activeObjectType)?.label ?? 'Database objects'} details`}>
            {#each metadataWarnings as warning}<p class="metadata-warning">{warning}</p>{/each}
            {#if selectedObject}
              {@const badge = accountBadge(selectedObject)}
              <div class="object-detail-card">
                <div class="object-heading">
                  <h1>{objectDisplayName(selectedObject)}</h1>
                  {#if badge}<small class="account-badge" title={badge.title}>{badge.label}</small>{/if}
                </div>
                {#if selectedObject.type === 'user' && selectedConnection.driver === 'mysql'}
                  <UserDetails user={selectedObject} />
                {:else if definitionLoading}
                  <p role="status">Loading SQL definition…</p>
                {:else if definitionError}
                  <p role="alert">{definitionError}</p>
                  <button onclick={() => selectedObject && selectObject(selectedObject)}>Retry SQL definition</button>
                {:else if selectedObject.definition}
                  <ObjectDefinition preserveSource={selectedConnection.driver !== 'mysql'} isView={isViewType(selectedObject.type)} definition={selectedObject.definition} name={objectDisplayName(selectedObject)} sqlMode={selectedObject.details?.['SQL mode']} />
                {:else}
                  <p>{selectedObject.details?.['Definition status'] || 'SQL definition is unavailable to this account.'}</p>
                {/if}
                {#if selectedObject.details && !(selectedObject.type === 'user' && selectedConnection.driver === 'mysql')}
                  <dl class="object-properties">
                    {#each Object.entries(selectedObject.details) as [label, value]}
                      <div><dt>{label}</dt><dd>{value}</dd></div>
                    {/each}
                  </dl>
                {/if}
                {#if selectedObject.type === 'role' && selectedConnection.driver === 'mysql'}
                  <p>Reconstructed CREATE ROLE; grants and original creation history are not included. Locked accounts without credentials are classified as roles.</p>
                {/if}
              </div>
            {:else}
              <div class="object-empty-state"><Database size={28} /><h1>{objectSections.find((section) => section.type === activeObjectType)?.label}</h1><p>Select an object from the left menu to view its details.</p></div>
            {/if}
          </section>
        {/if}
        </div>
      </section>
    {:else if loading}
      <section class="empty-state" aria-labelledby="empty-title">
        <div class="empty-icon"><Database size={24} strokeWidth={1.7} /></div>
        <h1 id="empty-title">Loading connections.</h1>
        <p>Reading saved profiles from <code>data/config.json</code>.</p>
      </section>
    {:else if error}
      <section class="empty-state" aria-labelledby="empty-title">
        <div class="empty-icon"><Database size={24} strokeWidth={1.7} /></div>
        <h1 id="empty-title">Could not load connections.</h1>
        <p>{error}</p>
        <button class="primary-button empty-action" onclick={() => void loadConnections()}>Try again</button>
      </section>
    {:else if connections.length === 0}
      <section class="empty-state" aria-labelledby="empty-title">
        <div class="empty-icon"><Database size={24} strokeWidth={1.7} /></div>
        <h1 id="empty-title">Connect your first database.</h1>
        {@render connectionInstructions()}
      </section>
    {:else}
      <section class="connections-view" aria-labelledby="connections-title">
        <div class="page-heading"><h1 id="connections-title">Connections</h1></div>
        <div class="connection-list">
          {#each connections as connection (connection.id)}
            <button class="connection-card" disabled={!!connection.configurationErrors?.length} onclick={() => void openDatabase(connection)} aria-label={connection.configurationErrors?.length ? `Invalid configuration for ${connection.name}: ${connection.configurationErrors.join("; ")}. Fix data/config.json and restart.` : `Open ${connection.name}`}>
              <div class="connection-row">
                <span class="connection-type"><ConnectionIcon driver={connection.driver} engine={connection.engine} /></span>
                <span class="connection-name" title={connection.name}>{connection.name}</span>
                {#if connection.driver !== 'sqlite'}<span class="connection-meta" title="Host and port"><Server size={14} /><span>{connection.host}:{connection.port}</span></span>
                <span class="connection-meta" title="Database user"><UserRound size={14} /><span>{connection.user}</span></span>
                {/if}
                <span class="connection-meta" title={connection.driver === 'sqlite' ? 'Database file' : 'Database'}><Database size={14} /><span>{connection.database}</span></span>
                <span class="connection-meta connection-size" title="Database size"><HardDrive size={14} /><span>{formatBytes(connection.databaseSizeBytes)}</span></span>
                <span class="connection-meta connection-time" title={connection.lastConnectedAt ? `Last connection · ${browserTimeZoneLabel(connection.lastConnectedAt)}` : 'Last connection'}><Clock size={14} /><span>{formatDateTime(connection.lastConnectedAt)}{#if connection.lastConnectedAt} <span class="timezone-badge">{browserTimeZoneLabel(connection.lastConnectedAt)}</span>{/if}</span></span>
                {#if connection.readonly}<span class="connection-readonly" title="Read-only connection"><ShieldCheck size={14} /> Read only</span>{/if}
                <span class:connected={connection.connected} class="preview-badge" role="img" aria-label={connection.connected ? 'Connected' : 'Not connected'} title={connection.connected ? 'Connected' : 'Not connected'}>{#if connection.connected}<Plug size={16} aria-hidden="true" />{:else}<Unplug size={16} aria-hidden="true" />{/if}</span>
              </div>
              {#if connection.configurationErrors?.length}
                <div class="connection-config-errors"><strong>Invalid configuration · fix data/config.json and restart</strong><ul>{#each connection.configurationErrors as problem}<li>{problem}</li>{/each}</ul></div>
              {/if}
            </button>
          {/each}
        </div>
      </section>
    {/if}
  </main>
</div>

{#if deletingRow && rowsPage && selectedConnection}
  <DeleteRecordDialog connectionId={selectedConnection.id} table={selectedTable} columns={rowsPage.columns} row={deletingRow} editKey={rowsPage.editKey ?? []} cancel={() => deletingRow = null} saved={async () => { const page = Math.min(currentPage, Math.max(1, Math.ceil(((rowsPage?.totalRows ?? 1) - 1) / pageSize))); deletingRow = null; await selectTable(relationName, page); }} />
{/if}
