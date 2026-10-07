import { describe, expect, it } from 'vitest'

import { category } from '@/test/builders'
import { facetCategories } from './facets'

const FOOD = category('food', 'Food & Dining')
const GROCERIES = category('groceries', 'Groceries', 'food')
const RESTAURANTS = category('restaurants', 'Restaurants', 'food')
const HOME = category('home', 'Home')
const SYSTEM = category('system', 'Balance Adjustment', null, { is_user_assignable: false })
const ALL = [FOOD, GROCERIES, RESTAURANTS, HOME, SYSTEM]

describe("the Categories facet's list", () => {
  it('offers only assignable categories', () => {
    expect(facetCategories(ALL).map((one) => one.id)).toEqual([
      'food',
      'groceries',
      'restaurants',
      'home',
    ])
  })
})
