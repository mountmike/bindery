import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string, opts?: unknown) => (typeof opts === 'string' ? opts : key) }),
}))

vi.mock('../../api/client', () => ({
  api: {
    listNotifications: vi.fn(),
    addNotification: vi.fn(),
    updateNotification: vi.fn(),
    deleteNotification: vi.fn(),
    testNotification: vi.fn(),
  },
}))

import { api, NotificationConfig } from '../../api/client'
import NotificationsTab from './NotificationsTab'

const stored: NotificationConfig = {
  id: 1,
  name: 'Discord',
  type: 'webhook',
  url: 'https://example.test/hook',
  topic: '',
  method: 'POST',
  headers: '{}',
  onGrab: true,
  onImport: true,
  onUpgrade: false,
  onFailure: true,
  onHealth: false,
  onBookAnnounced: false,
  onRequestFulfilled: true,
  enabled: true,
}

const toggle = (event: string) => screen.getByRole('button', { name: `settings.notifications.request${event}Toggle` })

beforeEach(() => {
  vi.clearAllMocks()
})

// #4054: approved, declined and fulfilled are each their own toggle, off for a
// new webhook, and saved with the rest of the triggers.
describe('NotificationsTab request lifecycle toggles', () => {
  it('are off for a new webhook and are sent when turned on', async () => {
    vi.mocked(api.listNotifications).mockResolvedValue([])
    vi.mocked(api.addNotification).mockResolvedValue({ ...stored, id: 2 })
    render(<NotificationsTab />)

    fireEvent.click(await screen.findByRole('button', { name: 'settings.notifications.addButton' }))
    for (const event of ['Approved', 'Declined', 'Fulfilled']) {
      expect(toggle(event)).toHaveAttribute('aria-pressed', 'false')
    }
    fireEvent.click(toggle('Approved'))
    fireEvent.click(toggle('Fulfilled'))
    fireEvent.change(screen.getByPlaceholderText('Webhook URL'), { target: { value: 'https://example.test/new' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(api.addNotification).toHaveBeenCalled())
    expect(vi.mocked(api.addNotification).mock.calls[0][0]).toMatchObject({
      onRequestApproved: true, onRequestDeclined: false, onRequestFulfilled: true,
    })
  })

  it('show a badge and keep the stored values when editing', async () => {
    vi.mocked(api.listNotifications).mockResolvedValue([stored])
    vi.mocked(api.updateNotification).mockResolvedValue(stored)
    render(<NotificationsTab />)

    expect(await screen.findByText('settings.notifications.onRequestFulfilled')).toBeInTheDocument()
    expect(screen.queryByText('settings.notifications.onRequestApproved')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'common.edit' }))
    expect(toggle('Fulfilled')).toHaveAttribute('aria-pressed', 'true')

    fireEvent.click(toggle('Fulfilled'))
    fireEvent.click(toggle('Declined'))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.updateNotification).toHaveBeenCalled())
    expect(vi.mocked(api.updateNotification).mock.calls[0][1]).toMatchObject({
      onRequestApproved: false, onRequestDeclined: true, onRequestFulfilled: false, onGrab: true,
    })
  })
})
