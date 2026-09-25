import { indentWithTab } from '@codemirror/commands'
import { yaml } from '@codemirror/lang-yaml'
import { indentUnit } from '@codemirror/language'
import { Compartment, EditorState } from '@codemirror/state'
import { oneDark } from '@codemirror/theme-one-dark'
import { EditorView, keymap } from '@codemirror/view'
import { basicSetup } from 'codemirror'
import { useEffect, useRef } from 'react'

interface Props {
  value: string
  onChange: (value: string) => void
  readOnly?: boolean
  /** CSS min-height of the editing area. */
  minHeight?: string
}

const darkQuery = '(prefers-color-scheme: dark)'

/** YAML editor (CodeMirror 6) for docker-compose files. Loaded lazily. */
export default function ComposeEditor({ value, onChange, readOnly = false, minHeight = '24rem' }: Props) {
  const hostRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const onChangeRef = useRef(onChange)
  const readOnlyConf = useRef(new Compartment())
  const themeConf = useRef(new Compartment())
  useEffect(() => {
    onChangeRef.current = onChange
  })

  // Create the editor once; later prop changes are applied below.
  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    const dark = window.matchMedia(darkQuery)
    const view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: value,
        extensions: [
          basicSetup,
          yaml(),
          indentUnit.of('  '),
          EditorState.tabSize.of(2),
          keymap.of([indentWithTab]),
          EditorView.lineWrapping,
          themeConf.current.of(dark.matches ? oneDark : []),
          readOnlyConf.current.of(EditorState.readOnly.of(readOnly)),
          EditorView.theme({
            '&': { fontSize: '13px' },
            '.cm-content, .cm-gutter': { minHeight },
            '.cm-scroller': {
              fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
            },
          }),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) onChangeRef.current(u.state.doc.toString())
          }),
        ],
      }),
    })
    viewRef.current = view
    const onScheme = () =>
      view.dispatch({
        effects: themeConf.current.reconfigure(dark.matches ? oneDark : []),
      })
    dark.addEventListener('change', onScheme)
    return () => {
      dark.removeEventListener('change', onScheme)
      view.destroy()
      viewRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- value/readOnly are synced by the effects below
  }, [minHeight])

  // External value changes (e.g. the stack loaded) replace the document.
  useEffect(() => {
    const view = viewRef.current
    if (view && view.state.doc.toString() !== value) {
      view.dispatch({
        changes: { from: 0, to: view.state.doc.length, insert: value },
      })
    }
  }, [value])

  useEffect(() => {
    viewRef.current?.dispatch({
      effects: readOnlyConf.current.reconfigure(EditorState.readOnly.of(readOnly)),
    })
  }, [readOnly])

  return (
    <div
      ref={hostRef}
      className="overflow-hidden rounded-md border border-zinc-300 focus-within:border-zinc-500 focus-within:ring-1 focus-within:ring-zinc-500 dark:border-zinc-700 [&_.cm-editor]:outline-none"
    />
  )
}
