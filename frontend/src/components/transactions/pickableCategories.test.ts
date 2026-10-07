import { describe, expect, it } from 'vitest'

import type { Category } from '@/lib/transactions/types'
import { category } from '@/test/builders'

import { pickableCategories } from './pickableCategories'

const FOOD = category('food', 'Food')
const GROCERIES = category('groceries', 'Groceries', 'food')
const HOBBIES = category('hobbies', 'Hobbies', null, { excluded_from_category_list: true })
const MODELS = category('models', 'Model trains', 'hobbies')
const OPENING = category('opening', 'Opening Balance', null, { is_user_assignable: false })
const ALL = [FOOD, GROCERIES, HOBBIES, MODELS, OPENING]

const ids = (list: Category[]) => list.map((one) => one.id)

describe('pickableCategories', () => {
  it('leaves out a category hidden from the category list, and its children', () => {
    expect(ids(pickableCategories(ALL, null))).toEqual(['food', 'groceries'])
  })

  it('keeps a hidden category that is the current value', () => {
    expect(ids(pickableCategories(ALL, 'models'))).toEqual(['food', 'groceries', 'models'])
  })
})
