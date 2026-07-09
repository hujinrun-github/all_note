import { type KeyboardEvent } from 'react'

export interface NoteData {
  id: string; title: string; folder_id: string; tags: string; updated_at: number
}

interface NoteCardProps {
  note: NoteData
  onOpen?: (note: NoteData) => void
}

export function NoteCard({ note, onOpen }: NoteCardProps) {
  const tags: string[] = JSON.parse(note.tags || '[]')

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (!onOpen) return
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      onOpen(note)
    }
  }

  return (
    <div
      className={`note-row-card ${onOpen ? 'is-clickable' : ''}`}
      role={onOpen ? 'button' : undefined}
      tabIndex={onOpen ? 0 : undefined}
      onClick={onOpen ? () => onOpen(note) : undefined}
      onKeyDown={handleKeyDown}
    >
      <strong className="text-[14px] leading-snug font-medium text-fs-text">{note.title}</strong>
      {tags.length > 0 && (
        <div className="flex gap-1 flex-wrap">
          {tags.map((tag: string) => (
            <span key={tag} className="text-fs-accent bg-fs-accent/8 rounded-md px-2 py-0.5 text-[11px] font-medium">{tag}</span>
          ))}
        </div>
      )}
    </div>
  )
}
