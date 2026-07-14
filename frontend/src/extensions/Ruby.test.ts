import { Editor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from 'tiptap-markdown'
import { describe, expect, it } from 'vitest'
import { Ruby } from './Ruby'

describe('Ruby extension', () => {
  it('parses Aozora-style furigana and serializes it without data loss', () => {
    const editor = new Editor({
      extensions: [StarterKit, Ruby, Markdown.configure({ html: false })],
      content: '駅の｜附近《ふきん》を歩く',
    })

    expect(editor.getHTML()).toContain('<ruby')
    expect(editor.getHTML()).toContain('<rb>附近</rb><rt>ふきん</rt>')

    const storage = editor.storage as unknown as Record<
      string,
      { getMarkdown: () => string } | undefined
    >
    expect(storage.markdown?.getMarkdown()).toBe('駅の｜附近《ふきん》を歩く')

    editor.destroy()
  })

  it('does not parse ruby syntax inside code blocks', () => {
    const editor = new Editor({
      extensions: [StarterKit, Ruby, Markdown.configure({ html: false })],
      content: '```text\n｜附近《ふきん》\n```',
    })

    expect(editor.getHTML()).not.toContain('<ruby')
    editor.destroy()
  })
})
