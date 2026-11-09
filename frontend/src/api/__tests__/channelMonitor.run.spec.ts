import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))

vi.mock('@/api/client', () => ({ apiClient: { post } }))

import { run } from '@/api/channelMonitor'
import { runNow } from '@/api/admin/channelMonitor'

describe('manual channel monitor requests', () => {
  beforeEach(() => {
    post.mockReset().mockResolvedValue({ data: { results: [] } })
  })

  it('allows the full execution budget for user checks', async () => {
    await run(42)
    expect(post).toHaveBeenCalledWith('/channel-monitors/42/run', undefined, {
      timeout: 120000,
    })
  })

  it('allows the same budget for admin checks and returns results', async () => {
    await expect(runNow(42)).resolves.toEqual({ results: [] })
    expect(post).toHaveBeenCalledWith('/admin/channel-monitors/42/run', undefined, {
      timeout: 120000,
    })
  })
})
