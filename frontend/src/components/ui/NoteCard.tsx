export interface NoteData {
  id: string; title: string; folder_id: string; tags: string; updated_at: number
}

export function NoteCard({ note }: { note: NoteData }) {
  const tags: string[] = JSON.parse(note.tags || '[]')

  return (
    <div className="grid gap-1 py-2">
      <strong className="text-[13px] leading-snug font-medium">{note.title}</strong>
      <div className="flex gap-1 flex-wrap mt-1">
        {tags.map((tag: string) => (
          <span key={tag} className="text-fs-accent bg-fs-accent/10 rounded-sm px-1.5 py-0.5 text-[11px] font-medium">{tag}</span>
        ))}
      </div>
    </div>
  )
}
