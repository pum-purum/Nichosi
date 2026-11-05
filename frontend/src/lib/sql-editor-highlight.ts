import type { Range } from '@codemirror/state';
import { Decoration, ViewPlugin, type DecorationSet, type EditorView, type ViewUpdate } from '@codemirror/view';
import { highlightSQL } from './sql-highlight';

function decorations(view: EditorView): DecorationSet {
  // Parse only escaped markup from our bundled highlighter, never raw SQL as HTML.
  const document = new DOMParser().parseFromString(`<div>${highlightSQL(view.state.doc.toString())}</div>`, 'text/html');
  const ranges: Range<Decoration>[] = [];
  let offset = 0;
  function visit(node: Node) {
    const from = offset;
    if (node.nodeType === Node.TEXT_NODE) offset += node.textContent?.length ?? 0;
    else for (const child of node.childNodes) visit(child);
    if (node instanceof Element && offset > from && node.className) {
      ranges.push(Decoration.mark({ class: node.className }).range(from, offset));
    }
  }
  visit(document.body);
  return Decoration.set(ranges, true);
}

// Match Table info tokenization as well as its palette, including built-ins and NULL.
export const sqlEditorHighlighting = ViewPlugin.fromClass(class {
  decorations: DecorationSet;
  constructor(view: EditorView) { this.decorations = decorations(view); }
  update(update: ViewUpdate) {
    if (update.docChanged) this.decorations = decorations(update.view);
  }
}, { decorations: plugin => plugin.decorations });
