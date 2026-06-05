export interface NoteData {
  id: string; title: string; folder_id: string; tags: string; updated_at: number
}

export function NoteCard({ note }: { note: NoteData }) {
  const tags: string[] = JSON.parse(note.tags || '[]')

  return (
    <div className="note-row-card">
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
