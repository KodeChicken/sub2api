import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import HelpTooltip from '@/components/common/HelpTooltip.vue'
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
  it('reveals temporary group names only through the clock tooltip', async () => {
    const wrapper = mount(AccountGroupsCell, {
      attachTo: document.body,
      props: {
        groups: [group(1, 'Permanent pool'), group(2, 'Second pool'), group(3, 'Third pool')],
        temporaryGroups: [group(4, 'Burst pool'), group(5, 'Overflow pool')],
        maxDisplay: 2,
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

    const tooltipTrigger = wrapper.getComponent(HelpTooltip)
    const clock = tooltipTrigger.get('[role="img"]')
    const tooltip = document.body.querySelector<HTMLElement>('[role="tooltip"]')!

    expect(wrapper.findAll('[role="img"]')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('Burst pool')
    expect(wrapper.text()).not.toContain('Overflow pool')
    expect(wrapper.get('[data-test="bound-group"]').text()).toBe('Permanent pool')
    expect(tooltip.style.display).toBe('none')

    await tooltipTrigger.trigger('mouseenter')
    expect(tooltip.style.display).not.toBe('none')
    expect(tooltip.textContent).toContain('Burst pool')
    expect(tooltip.textContent).toContain('Overflow pool')
    await tooltipTrigger.trigger('mouseleave')
    expect(tooltip.style.display).toBe('none')

    await clock.trigger('focusin')
    expect(tooltip.style.display).not.toBe('none')
    await clock.trigger('keydown', { key: 'Escape' })
    expect(tooltip.style.display).toBe('none')

    expect(wrapper.get('button').text()).toBe('+2')
    await wrapper.get('button').trigger('click')
    const popover = document.body.querySelector<HTMLElement>('.min-w-48')!
    expect(popover.textContent).toContain('Second pool')
    expect(popover.textContent).not.toContain('Burst pool')
    expect(popover.textContent).not.toContain('Overflow pool')

    wrapper.unmount()
  })
})
