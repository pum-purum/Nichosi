# Design direction

Nichosi is a quiet, local-first database workspace with an original visual identity. The approved table workspace is the baseline for future database screens. A user-provided reference informed its broad layout and information density; do not copy another product's branding, icons, or distinctive visual details.

## Table workspace

- Use a compact top bar for row search, database and schema breadcrumbs, and a small set of contextual actions.
- Keep a narrow app navigation rail beside a database-object tree. The object tree shows the active connection, database, schema, and tables.
- Show each table’s combined data and index size in the sidebar with a proportional bar. Scale all bars against the largest table in the database, keeping the scale stable while filtering; zero-size tables have an empty bar.
- Give the data grid most of the available space. Keep row actions at the far right, column names and types in the header, and pagination and row counts in a quiet footer on the workspace background, without a surrounding panel or border.
- Size the scrollable data area to its contents up to the available height, with all four corners rounded. Short tables end immediately after their last row; keep a small gap before the pagination footer.
- Show data columns before row actions, without a separate row-number column. Use blue for the edit icon and red for the delete icon.
- Prefer clear, practical labels and familiar line icons. Use Lucide or Tabler icons and check the package license before redistributing bundled assets.
- Use restrained borders, subtle hover states, and rounded controls. Avoid decorative glow, strong gradients, and oversized hero sections inside data-workspace screens.

## Color and type

- Use neutral graphite surfaces as the default dark theme. Avoid a green cast across backgrounds and panels.
- Use soft grass-green (`#83c96f`) for focus, object icons, size bars, pagination, and primary actions. Use the same green accent for the selected object in the sidebar list and the active icon in the far-left navigation rail. Give the active rail icon a restrained tinted background and an inset marker. Keep icons, labels, and `aria-current` so selection never relies on color alone. Keep the accents distinct from neutral surfaces.
- Use readable system typography. Table content should remain compact enough for scanning while labels and controls stay comfortably legible; avoid tiny metadata text.
- Preserve visible keyboard focus, sufficient contrast, and clear disabled states. Do not communicate state by color alone.

## Product behavior

- Keep the connections screen focused on saved connections. When none exist, show the first-connection guidance; when connections exist, show their database type, size, last connection, and an unavailable state where metadata cannot be provided.
- Visual prototypes must be explicitly labeled. Do not imply that database features work until they are connected to the API.
- Do not use third-party screenshots, logos, product names, or distinctive layouts as design targets. Use any user-provided reference only for high-level layout guidance unless the user explicitly asks otherwise.

Table pagination immediately shows a spinner inside the clicked page or arrow button for at least 200 ms, including failed requests. The current page and table rows update together after loading succeeds. Pagination and page-size controls remain disabled while rows load. During any table data refresh, blur and dim the table and disable interaction with its stale contents. Table selection also uses a translucent loading overlay.

## Table value types

Cells derive their appearance from the column SQL type, never from the column position or text content. Keep the grid quiet: integers use blue, decimals green, strings white, JSON lilac, dates bright cyan, `ENUM` yellow, `SET` lilac, and boolean values purple. Unknown types use the normal text color. Numbers retain their original precision; dates retain the server representation and timezone, with a browser-local value in the tooltip. MySQL `tinyint(1)` is treated as boolean.

SQL NULL is displayed as `NULL`. Text remains unquoted. `ENUM` values use one badge, while each selected `SET` value gets its own badge. Tooltips and accessible labels describe the SQL type and value.

## Table information

Keep a bottom tab strip with Data and Table info beneath the table workspace. Use adjoining rectangular tabs on a shared border; connect the active tab to the content surface and mark its bottom edge with the accent color. Blend the active tab background from a raised surface at the bottom to the current content background at the top. Round the bottom tab corners slightly and use small concave curves where the active tab joins the panel. The metadata panel has square bottom corners and shares a single border with the tab strip, with no divider above the active tab. Inactive tabs show only their label, without icons, backgrounds, or individual borders. Avoid separate rounded button styling. Table info loads on demand for the selected connection and table, separately from row loading, and can be refreshed by reloading the browser page. Show storage and engine details, estimated sizes and row counts, columns, primary/unique and other indexes, foreign keys, triggers, and the complete read-only CREATE TABLE statement with copy support. Display explicit warnings for unavailable or truncated metadata. Keep both tabs keyboard accessible and discard stale responses when switching tables or connections.

Within Table info, show column properties in this order: Name, Type, Default, Nullable, then supplementary metadata. Display each key or index once, with its name first and its ordered column list next; preserve prefix lengths and descending ordering. Group composite foreign keys with aligned source and referenced columns. Format CREATE TABLE with one column or constraint per indented line, keeping nested expressions and literals intact, and put storage options on separate lines.

## View workspace

Views share the table data workspace, including typed cells, loading overlays, row count, page-size controls, and pagination. Use the sidebar selection to identify the current view, without a duplicate name or read-only banner above the workspace; editing stays disabled regardless of connection settings. Use Data and View info tabs at the bottom. Load view metadata only when opened, showing properties, ordered column metadata, and formatted CREATE VIEW with copy support. Omit the Actions column from view rows and the redundant View information / Read-only caption from view metadata. Refresh metadata by reloading the browser page, without a separate Refresh button. Do not show table storage estimates or empty table-only key/trigger sections. Guard row and metadata responses when switching objects or connections.

## Sidebar object filtering

Filter every object category immediately by name, ignoring case and surrounding whitespace. All whitespace-separated search words must match; account searches include the display identity and the server-reported name/host. Keep punctuation literal. Show matching/total counts and an explicit empty result message. Provide a clear button and Escape shortcut, retain the current detail selection while filtering, and reset the query when changing sections or connections. Table size scales remain based on all tables.
