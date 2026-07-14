import type { EventData } from '../components/ui/EventChip'
import type { NoteData } from '../components/ui/NoteCard'
import type { TaskData } from '../components/ui/TaskRow'
import { api } from './client'

export interface TodayOverview {
  todayTasks: TaskData[]
  overdueTasks: TaskData[]
  events: EventData[]
  recentNotes: NoteData[]
}

export async function getTodayOverview() {
  const response = await api.get<TodayOverview>('/api/today')
  return response.data
}
