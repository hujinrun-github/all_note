import { createBrowserRouter } from 'react-router-dom'
import { App } from './App'
import { lazy } from 'react'

const Dashboard = lazy(() => import('./routes/Dashboard'))
const Login = lazy(() => import('./routes/Login'))
const Notes = lazy(() => import('./routes/Notes'))
const Editor = lazy(() => import('./routes/Editor'))
const Tasks = lazy(() => import('./routes/Tasks'))
const Calendar = lazy(() => import('./routes/Calendar'))
const Inbox = lazy(() => import('./routes/Inbox'))
const Search = lazy(() => import('./routes/Search'))

export const router = createBrowserRouter(
  [
    { path: '/login', element: <Login /> },
    {
      path: '/',
      element: <App />,
      children: [
        { index: true, element: <Dashboard /> },
        { path: 'notes', element: <Notes /> },
        { path: 'editor/:id', element: <Editor /> },
        { path: 'tasks', element: <Tasks /> },
        { path: 'calendar', element: <Calendar /> },
        { path: 'inbox', element: <Inbox /> },
        { path: 'search', element: <Search /> },
      ],
    },
  ],
  { basename: import.meta.env.BASE_URL },
)
