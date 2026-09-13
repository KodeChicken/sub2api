import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import type { Group } from '@/types'
import AccountGroupsCell from '../AccountGroupsCell.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

const group = (id: number, name: string) => ({
  id,
  name,
  platform: 'openai',
  subscription_type: 'standard',
  rate_multiplier: 1,
}) as Group

describe('AccountGroupsCell', () => {
  it('renders temporary and permanently bound groups with distinct badges', () => {
    const wrapper = mount(AccountGroupsCell, {
      props: {
        groups: [group(1, 'Permanent pool')],
        temporaryGroups: [group(2, 'Burst pool')],
      },
      global: {
        stubs: {
          GroupBadge: {
            props: ['name'],
            template: '<span data-test="bound-group">{{ name }}</span>',
          },
        },
      },
    })

    expect(wrapper.text()).toContain('admin.accounts.temporaryGroupPrefix · Burst pool')
    expect(wrapper.get('[data-test="bound-group"]').text()).toBe('Permanent pool')
    expect(wrapper.html()).toContain('border-amber-300')
  })
})
