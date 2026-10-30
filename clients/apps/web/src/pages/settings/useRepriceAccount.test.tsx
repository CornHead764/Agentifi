import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { withProgressToast, type RunWithProgress, type ToastOptions } from '@/components/ui'
import { useRepricing, type ValuationResults } from '@/lib/clients/connections'
import { parseMoney } from '@/lib/money'

import { repriceWithToast } from './useRepriceAccount'

const CAR = { name: 'Blue Roadster', currency: 'USD' }

function recorder() {
  const shown: ToastOptions[] = []
  const updates: [number, ToastOptions][] = []
  const toast = {
    show: (options: ToastOptions) => shown.push(options),
    update: (id: number, options: ToastOptions) => {
      updates.push([id, options])
    },
  }
  const progress: RunWithProgress = (loading, work, outcome, failure) =>
    withProgressToast(toast, loading, work, outcome, failure)
  return { shown, updates, progress }
}

const priced: ValuationResults = {
  results: [
    {
      account_id: 'car-a',
      name: 'Blue Roadster',
      skipped: null,
      source: 'kbb',
      estimate: parseMoney('17500.00'),
      adjustment: parseMoney('500.00'),
      mileage_used: 41000,
      priced_as: {
        year: '2019',
        make: 'Examplemotors',
        model: 'Roadster',
        trim: null,
        mileage: 41000,
        typical_mileage: false,
        address: null,
        low: null,
        high: null,
      },
    },
  ],
}

describe('re-pricing one asset', () => {
  it('says it is re-pricing, then what it priced, in the same toast', async () => {
    const toast = recorder()
    await repriceWithToast(toast.progress, CAR, Promise.resolve(priced))
    expect(toast.shown).toEqual([{ title: 'Re-pricing Blue Roadster…', tone: 'loading' }])
    expect(toast.updates).toEqual([
      [
        1,
        {
          title: 'Blue Roadster re-priced',
          description:
            'Now $17,500.00, +$500.00 on its balance. Priced as a 2019 Examplemotors Roadster at 41,000 miles.',
          tone: 'success',
        },
      ],
    ])
  })

  it('turns the loading toast into the failure, with no second toast', async () => {
    const toast = recorder()
    await repriceWithToast(toast.progress, CAR, Promise.reject(new Error('The lookup timed out')))
    expect(toast.shown).toHaveLength(1)
    expect(toast.updates).toEqual([
      [
        1,
        { title: 'Blue Roadster was not re-priced', description: 'The lookup timed out', tone: 'error' },
      ],
    ])
  })
})

describe('whether a re-price is running', () => {
  function running(start: { key: string[]; variables?: string }, accountId?: string): string {
    const client = new QueryClient()
    void client
      .getMutationCache()
      .build(client, { mutationKey: start.key, mutationFn: () => new Promise(() => {}) })
      .execute(start.variables)
    function Probe() {
      return <>{String(useRepricing(accountId))}</>
    }
    return renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <Probe />
      </QueryClientProvider>,
    )
  }

  it('sees a re-price of the same asset, from whichever menu started it', () => {
    expect(running({ key: ['revalue', 'one'], variables: 'car-a' }, 'car-a')).toBe('true')
    expect(running({ key: ['revalue', 'one'], variables: 'car-b' }, 'car-a')).toBe('false')
  })

  it('counts every asset as re-pricing while "Re-price all" runs', () => {
    expect(running({ key: ['revalue', 'all'] }, 'car-a')).toBe('true')
    expect(running({ key: ['revalue', 'all'] })).toBe('true')
    expect(running({ key: ['revalue', 'one'], variables: 'car-a' })).toBe('false')
  })
})
