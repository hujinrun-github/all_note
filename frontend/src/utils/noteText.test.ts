import { describe, expect, it } from 'vitest'
import { markdownToPlainText } from './noteText'

describe('markdownToPlainText', () => {
  it('removes ordered-list markers and empty list items from note previews', () => {
    expect(
      markdownToPlainText('1. すぐ近く、日本語を勉強する。\n2.')
    ).toBe('すぐ近く、日本語を勉強する。')
  })

  it('keeps ruby base text without exposing Aozora syntax or readings', () => {
    expect(
      markdownToPlainText(
        '1. すぐ｜近《ちか》く、｜日本語《にほんご》を｜勉強《べんきょう》する。'
      )
    ).toBe('すぐ近く、日本語を勉強する。')
  })

  it('keeps readable spacing between Markdown blocks', () => {
    expect(markdownToPlainText('第一段。\n\n第二段。')).toBe('第一段。 第二段。')
  })
})
