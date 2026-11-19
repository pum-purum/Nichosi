import { EditorView } from '@codemirror/view';

export const sqlCompletionTheme = EditorView.theme({
  '.cm-tooltip.cm-tooltip-autocomplete': {
    minWidth: 'min(340px, calc(100vw - 32px))',
    maxWidth: 'min(560px, calc(100vw - 32px))',
    padding: '6px',
    border: '1px solid var(--line)',
    borderRadius: '8px',
    backgroundColor: 'var(--surface)',
    color: 'var(--text)',
    boxShadow: '0 12px 32px #0006',
    overflow: 'hidden',
  },
  '.cm-tooltip.cm-tooltip-autocomplete > ul': {
    fontFamily: 'var(--mono)',
    fontSize: '15px',
    lineHeight: '24px',
    maxHeight: '288px',
  },
  '.cm-tooltip.cm-tooltip-autocomplete > ul > li': {
    display: 'flex',
    alignItems: 'center',
    gap: '10px',
    minHeight: '40px',
    padding: '8px 12px',
    borderRadius: '5px',
    cursor: 'pointer',
  },
  '.cm-tooltip.cm-tooltip-autocomplete > ul > li:hover': {
    backgroundColor: 'var(--surface-raised)',
  },
  '.cm-tooltip.cm-tooltip-autocomplete > ul > li[aria-selected]': {
    backgroundColor: 'color-mix(in srgb, var(--accent) 15%, var(--surface))',
    color: 'var(--text)',
    boxShadow: 'inset 3px 0 var(--accent)',
  },
  '.cm-completionIcon': { width: '18px', padding: '0', opacity: '1', color: 'var(--accent)' },
  '.cm-completionLabel': { flex: '1', overflow: 'hidden', textOverflow: 'ellipsis' },
  '.cm-completionMatchedText': { color: 'var(--accent)', fontWeight: '700', textDecoration: 'none' },
  '.cm-completionDetail': { marginLeft: '12px', fontFamily: 'var(--sans)', fontSize: '13px', fontStyle: 'normal', color: 'var(--muted)' },
});
