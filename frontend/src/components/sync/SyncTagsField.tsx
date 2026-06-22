import { useMemo, useState } from 'react'
import { parseSyncTagsInput } from './syncTagInput'

type SyncTagsFieldProps = {
  value: string
  onChange: (value: string) => void
}

export function SyncTagsField({ value, onChange }: SyncTagsFieldProps) {
  const tags = useMemo(() => parseSyncTagsInput(value), [value])
  const [draft, setDraft] = useState('')

  function commitDraft(input = draft) {
    const nextTags = mergeTags(tags, parseSyncTagsInput(input))
    onChange(nextTags.join(', '))
    setDraft('')
  }

  function handleRemove(tagToRemove: string) {
    const nextTags = tags.filter(
      (tag) => tag.toLowerCase() !== tagToRemove.toLowerCase()
    )
    onChange(nextTags.join(', '))
  }

  return (
    <div className="sync-field sync-tags-field">
      <div className="sync-field-heading">
        <span>同步标签过滤</span>
        <span className="sync-tags-status">
          {tags.length > 0 ? `${tags.length} 个标签` : '必填'}
        </span>
      </div>
      <p className="sync-field-help">只同步包含以下任一标签的笔记</p>
      <p className="sync-field-help sync-field-help-muted">
        至少填写一个同步标签
      </p>
      <div
        className="sync-tag-input-box"
        role="group"
        aria-label="同步标签输入框"
        onClick={(event) => {
          const input = event.currentTarget.querySelector('input')
          input?.focus()
        }}
      >
        {tags.map((tag) => (
          <button
            type="button"
            className="sync-tag-chip"
            key={tag.toLowerCase()}
            onClick={() => handleRemove(tag)}
            aria-label={`移除同步标签 ${tag}`}
          >
            <span>#{tag}</span>
            <span aria-hidden="true">×</span>
          </button>
        ))}
        <input
          aria-label="添加同步标签"
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onBlur={() => {
            if (draft.trim()) commitDraft()
          }}
          onKeyDown={(event) => {
            if (event.key === 'Enter' || event.key === ',' || event.key === '，') {
              event.preventDefault()
              commitDraft()
              return
            }
            if (event.key === 'Backspace' && !draft && tags.length > 0) {
              event.preventDefault()
              handleRemove(tags[tags.length - 1])
            }
          }}
          placeholder={tags.length > 0 ? '继续输入标签' : '输入标签后按回车'}
        />
      </div>
      {tags.length === 0 && (
        <span className="sync-tag-empty">还没有选择同步标签</span>
      )}
    </div>
  )
}

function mergeTags(currentTags: string[], incomingTags: string[]) {
  const seen = new Set<string>()
  const merged: string[] = []

  for (const tag of [...currentTags, ...incomingTags]) {
    const key = tag.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    merged.push(tag)
  }

  return merged
}
