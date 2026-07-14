import { Node as TiptapNode, mergeAttributes } from '@tiptap/react'
import type { MarkdownNodeSpec } from 'tiptap-markdown'

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
        if (!child.parentElement?.closest('code, pre, ruby'))
          textNodes.push(child as Text)
        return
      }
      collect(child)
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
      ruby.dataset.ruby = 'true'
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

export const Ruby = TiptapNode.create({
  name: 'ruby',
  group: 'inline',
  inline: true,
  atom: true,
  selectable: true,

  addAttributes() {
    return {
      base: {
        default: '',
        parseHTML: (element) => textWithoutReading(element as HTMLElement),
      },
      reading: {
        default: '',
        parseHTML: (element) =>
          (element as HTMLElement).querySelector('rt')?.textContent ?? '',
      },
    }
  },

  parseHTML() {
    return [{ tag: 'ruby' }]
  },

  renderHTML({ node, HTMLAttributes }) {
    return [
      'ruby',
      mergeAttributes(HTMLAttributes, { 'data-ruby': 'true' }),
      ['rb', {}, String(node.attrs.base ?? '')],
      ['rt', {}, String(node.attrs.reading ?? '')],
    ]
  },

  renderText({ node }) {
    return String(node.attrs.base ?? '')
  },

  addStorage(): { markdown: MarkdownNodeSpec } {
    return {
      markdown: {
        serialize(state, node) {
          state.write(
            `｜${String(node.attrs.base ?? '')}《${String(node.attrs.reading ?? '')}》`
          )
        },
        parse: {
          updateDOM(element) {
            replaceRubySyntax(element)
          },
        },
      },
    }
  },
})
