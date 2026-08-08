import { Editor, Node as TiptapNode, mergeAttributes } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import Placeholder from '@tiptap/extension-placeholder'
import { Markdown, type MarkdownNodeSpec } from 'tiptap-markdown'

declare global {
  interface Window {
    flowspaceContext?: { noteID: string; generation: string }
    flowspaceNative?: {
      configure(noteID: string, generation: string): void
      setMarkdown(markdown: string, generation: string): void
      command(command: string, value?: string): void
      replaceSelection(text: string, expectedText: string): boolean
      find(query: string, backwards: boolean): boolean
      focus(): void
    }
    webkit?: {
      messageHandlers?: {
        flowspace?: { postMessage(message: Record<string, unknown>): void }
      }
    }
  }
}

const RUBY_PATTERN = /｜([^《\n]+)《([^》\n]+)》/g

function textWithoutReading(element: HTMLElement) {
  const clone = element.cloneNode(true) as HTMLElement
  clone.querySelectorAll('rt').forEach((reading) => reading.remove())
  return clone.textContent ?? ''
}

function replaceRubySyntax(root: HTMLElement) {
  const textNodes: Text[] = []
  function collect(node: Node) {
    node.childNodes.forEach((child) => {
      if (child.nodeType === globalThis.Node.TEXT_NODE) {
        if (!child.parentElement?.closest('code, pre, ruby')) textNodes.push(child as Text)
      } else {
        collect(child)
      }
    })
  }
  collect(root)
  textNodes.forEach((textNode) => {
    const text = textNode.textContent ?? ''
    RUBY_PATTERN.lastIndex = 0
    if (!RUBY_PATTERN.test(text)) return
    RUBY_PATTERN.lastIndex = 0
    const fragment = document.createDocumentFragment()
    let cursor = 0
    for (const match of text.matchAll(RUBY_PATTERN)) {
      const index = match.index ?? 0
      if (index > cursor) fragment.append(text.slice(cursor, index))
      const ruby = document.createElement('ruby')
      const base = document.createElement('rb')
      const reading = document.createElement('rt')
      base.textContent = match[1]
      reading.textContent = match[2]
      ruby.append(base, reading)
      fragment.append(ruby)
      cursor = index + match[0].length
    }
    if (cursor < text.length) fragment.append(text.slice(cursor))
    textNode.replaceWith(fragment)
  })
}

const Ruby = TiptapNode.create({
  name: 'ruby',
  group: 'inline',
  inline: true,
  atom: true,
  selectable: true,
  addAttributes() {
    return {
      base: { default: '', parseHTML: (element) => textWithoutReading(element as HTMLElement) },
      reading: {
        default: '',
        parseHTML: (element) => (element as HTMLElement).querySelector('rt')?.textContent ?? '',
      },
    }
  },
  parseHTML() { return [{ tag: 'ruby' }] },
  renderHTML({ node, HTMLAttributes }) {
    return [
      'ruby',
      mergeAttributes(HTMLAttributes, { 'data-ruby': 'true' }),
      ['rb', {}, String(node.attrs.base ?? '')],
      ['rt', {}, String(node.attrs.reading ?? '')],
    ]
  },
  renderText({ node }) { return String(node.attrs.base ?? '') },
  addStorage(): { markdown: MarkdownNodeSpec } {
    return {
      markdown: {
        serialize(state, node) {
          state.write(`｜${String(node.attrs.base ?? '')}《${String(node.attrs.reading ?? '')}》`)
        },
        parse: { updateDOM(element) { replaceRubySyntax(element) } },
      },
    }
  },
})

let suppressChanges = false
const bubble = document.querySelector<HTMLDivElement>('#bubble')!
const editor = new Editor({
  element: document.querySelector('#editor')!,
  extensions: [
    StarterKit.configure({ heading: { levels: [1, 2, 3] } }),
    Placeholder.configure({ placeholder: '开始书写…' }),
    Ruby,
    Markdown.configure({ html: false, breaks: true, linkify: true }),
  ],
  content: '',
  editorProps: { attributes: { spellcheck: 'false', autocorrect: 'on' } },
  onUpdate() {
    if (suppressChanges) return
    post('change', { markdown: markdown(), wordCount: wordCount(markdown()) })
  },
  onSelectionUpdate() {
    const selectedText = currentSelectionText()
    post('selection', { selectedText })
    updateBubble(selectedText)
  },
  onBlur() { bubble.classList.remove('visible') },
})

function markdown() {
  const storage = editor.storage as unknown as Record<string, { getMarkdown: () => string } | undefined>
  return storage.markdown?.getMarkdown() ?? ''
}

function currentSelectionText() {
  const { from, to, empty } = editor.state.selection
  return empty ? '' : editor.state.doc.textBetween(from, to, '\n')
}

function post(type: string, fields: Record<string, unknown> = {}) {
  const context = window.flowspaceContext
  window.webkit?.messageHandlers?.flowspace?.postMessage({
    type,
    noteID: context?.noteID ?? '',
    generation: context?.generation ?? '',
    ...fields,
  })
}

function run(command: string, value = '') {
  const chain = editor.chain().focus()
  switch (command) {
    case 'bold': chain.toggleBold().run(); break
    case 'italic': chain.toggleItalic().run(); break
    case 'strike': chain.toggleStrike().run(); break
    case 'heading1': chain.toggleHeading({ level: 1 }).run(); break
    case 'heading2': chain.toggleHeading({ level: 2 }).run(); break
    case 'heading3': chain.toggleHeading({ level: 3 }).run(); break
    case 'bulletList': chain.toggleBulletList().run(); break
    case 'orderedList': chain.toggleOrderedList().run(); break
    case 'blockquote': chain.toggleBlockquote().run(); break
    case 'codeBlock': chain.toggleCodeBlock().run(); break
    case 'horizontalRule': chain.setHorizontalRule().run(); break
    case 'insertText': chain.insertContent(value).run(); break
  }
  updateBubble(currentSelectionText())
}

function updateBubble(selectedText: string) {
  if (!selectedText || !editor.isFocused) {
    bubble.classList.remove('visible')
    return
  }
  const { from, to } = editor.state.selection
  const start = editor.view.coordsAtPos(from)
  const end = editor.view.coordsAtPos(to)
  bubble.style.left = `${Math.max(8, (start.left + end.right) / 2 - bubble.offsetWidth / 2)}px`
  bubble.style.top = `${Math.max(8, Math.min(start.top, end.top) - 42)}px`
  bubble.classList.add('visible')
  bubble.querySelectorAll<HTMLButtonElement>('button').forEach((button) => {
    const command = button.dataset.command ?? ''
    const active = command.startsWith('heading')
      ? editor.isActive('heading', { level: Number(command.slice(-1)) })
      : editor.isActive(command)
    button.classList.toggle('active', active)
  })
}

bubble.addEventListener('mousedown', (event) => {
  event.preventDefault()
  const button = (event.target as HTMLElement).closest<HTMLButtonElement>('button[data-command]')
  if (button?.dataset.command) run(button.dataset.command)
})

window.flowspaceNative = {
  configure(noteID, generation) {
    window.flowspaceContext = { noteID, generation }
  },
  setMarkdown(value, generation) {
    if (generation !== window.flowspaceContext?.generation || value === markdown()) return
    suppressChanges = true
    editor.commands.setContent(value || '', { emitUpdate: false })
    suppressChanges = false
    post('documentState', { wordCount: wordCount(markdown()) })
  },
  command: run,
  replaceSelection(text, expectedText) {
    if (currentSelectionText() !== expectedText) return false
    editor.chain().focus().insertContent(text).run()
    return true
  },
  find(query, backwards) {
    if (!query) return false
    return window.find(query, false, backwards, true, false, false, false)
  },
  focus() { editor.commands.focus() },
}

document.addEventListener('keydown', (event) => {
  if (!event.metaKey) return
  const key = event.key.toLowerCase()
  if (key === 's') { event.preventDefault(); post('save') }
  if (key === 'k' && !event.shiftKey) { event.preventDefault(); post('globalSearch') }
  if (key === 'f') { event.preventDefault(); post('find') }
  if (key === 'x' && event.shiftKey) { event.preventDefault(); run('strike') }
})

function wordCount(value: string) {
  const text = value.replace(/[#*`~>\n[\]()!|-]/g, ' ').trim()
  if (!text) return 0
  const cjk = (text.match(/[\u4e00-\u9fff]/g) || []).length
  const latin = text.replace(/[\u4e00-\u9fff]/g, ' ').split(/\s+/).filter(Boolean).length
  return cjk + latin
}

post('ready')
