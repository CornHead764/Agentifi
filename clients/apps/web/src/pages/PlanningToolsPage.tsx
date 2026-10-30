import { useState, type ChangeEvent } from 'react'

import { BandedProjection } from '@/components/charts'
import { InfoTip } from '@/components/InfoTip'
import { Stat } from '@/components/figures'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Badge,
  Button,
  Card,
  Checkbox,
  Field,
  Input,
  MoneyInput,
  PageHeader,
  Tabs,
  TabsList,
  TabsTrigger,
} from '@/components/ui'
import {
  EMPTY_ADVANCED_INPUTS,
  EMPTY_RETIREMENT_INPUTS,
  useAdvancedRetirementProjection,
  useRetirementProjection,
  type AdvancedRetirementInputs,
  type RetirementAssumptions,
  type RetirementInputs,
  type RetirementProjection,
} from '@/lib/clients/planning'
import type { WireRate } from '@/lib/clients/entities'
import { formatPercent, parseRate, plural } from '@/lib/format'
import { amountFieldError, moneyToNumber, type Money as MoneyValue, amountToWire } from '@/lib/money'
import { useSettled } from '@/lib/useSettled'

/**
 * Planning Tools. **Every assumption is on screen and editable**, read off
 * the response rather than the form, so the printed figures are the ones the
 * projection used; the panel is labelled a projection, not a forecast.
 *
 * No credit score: there is no bureau feed to show one from.
 */
export function PlanningToolsPage() {
  return (
    <div className="page page--wide">
      <RetirementPlanner />
    </div>
  )
}

/**
 * One assumption's field. `used` reads the figure the server used off the
 * response, so a field left blank shows it instead of reading as a zero.
 */
interface Assumption<K extends string> {
  key: K
  label: string
  kind: 'int' | 'money' | 'rate'
  hint?: string | ((assumptions: RetirementAssumptions | undefined) => string)
  used: (assumptions: RetirementAssumptions | undefined) => string
}

/** Each inner list is one row of fields. */
type AssumptionRows<K extends string> = readonly (readonly Assumption<K>[])[]

type SharedKey = keyof RetirementInputs & keyof AdvancedRetirementInputs

const AGES: readonly Assumption<SharedKey>[] = [
  { key: 'currentAge', label: 'Your age', kind: 'int', used: (a) => placeholder(a?.current_age) },
  {
    key: 'retirementAge',
    label: 'Retirement age',
    kind: 'int',
    used: (a) => placeholder(a?.retirement_age),
  },
  {
    key: 'lifeExpectancy',
    label: 'Life expectancy',
    kind: 'int',
    hint: 'The chart runs to it.',
    used: (a) => placeholder(a?.life_expectancy),
  },
]

const LIVING: readonly Assumption<SharedKey>[] = [
  {
    key: 'annualLivingExpenses',
    label: 'Annual living expenses',
    kind: 'money',
    hint: "In retirement, today's money.",
    used: (a) => amountPlaceholder(a?.annual_living_expenses),
  },
  {
    key: 'annualRetirementIncome',
    label: 'Retirement income',
    kind: 'money',
    hint: 'Pensions, social security — a year.',
    used: (a) => amountPlaceholder(a?.annual_retirement_income),
  },
]

const TARGET: Assumption<SharedKey> = {
  key: 'targetAnnualIncome',
  label: 'Income you want',
  kind: 'money',
  hint: "A year, in today's money. Leave it empty for no target.",
  used: () => 'No target',
}

const INFLATION: Assumption<SharedKey> = {
  key: 'annualInflationPercent',
  label: 'Inflation %',
  kind: 'rate',
  used: (a) => ratePlaceholder(a?.annual_inflation),
}

const WITHDRAWAL: Assumption<SharedKey> = {
  key: 'withdrawalRatePercent',
  label: 'Withdrawal rate %',
  kind: 'rate',
  hint: 'What you draw in the first year of retirement.',
  used: (a) => ratePlaceholder(a?.withdrawal_rate),
}

const BASIC: AssumptionRows<keyof RetirementInputs> = [
  AGES,
  [
    {
      key: 'currentBalance',
      label: 'Current investments',
      kind: 'money',
      hint: (a) =>
        a?.is_balance_from_accounts
          ? 'From your investment accounts, including the cash in them.'
          : 'Your own figure. Clear it to use the accounts.',
      used: (a) => amountPlaceholder(a?.current_balance),
    },
  ],
  [
    {
      key: 'monthlyContribution',
      label: 'Monthly contribution',
      kind: 'money',
      hint: 'Added at the end of each month.',
      used: (a) => amountPlaceholder(a?.monthly_contribution),
    },
  ],
  [
    {
      key: 'annualReturnPercent',
      label: 'Annual return %',
      kind: 'rate',
      hint: 'Before inflation.',
      used: (a) => ratePlaceholder(a?.annual_return),
    },
    INFLATION,
  ],
  [WITHDRAWAL],
  LIVING,
  [
    {
      key: 'preRetirementTaxPercent',
      label: 'Tax rate before %',
      kind: 'rate',
      hint: 'Discounts the return while working.',
      used: (a) => ratePlaceholder(a?.pre_retirement_tax_rate),
    },
    {
      key: 'postRetirementTaxPercent',
      label: 'Tax rate after %',
      kind: 'rate',
      hint: 'Discounts it in retirement.',
      used: (a) => ratePlaceholder(a?.post_retirement_tax_rate),
    },
  ],
  [TARGET],
]

/** The same household split by tax treatment. */
const ADVANCED: AssumptionRows<keyof AdvancedRetirementInputs> = [
  AGES,
  [
    {
      key: 'taxableBalance',
      label: 'Already-taxed balance',
      kind: 'money',
      hint: (a) =>
        a?.advanced?.is_balance_from_accounts
          ? 'Brokerage, Roth, HSA — split from your accounts.'
          : 'Your own figure. Clear both to go back to the accounts.',
      used: (a) => amountPlaceholder(a?.advanced?.taxable_balance),
    },
    {
      key: 'deferredBalance',
      label: 'Tax-deferred balance',
      kind: 'money',
      hint: '401(k), IRA — taxed on the way out.',
      used: (a) => amountPlaceholder(a?.advanced?.deferred_balance),
    },
  ],
  [
    {
      key: 'annualTaxableContribution',
      label: 'Taxable contributions',
      kind: 'money',
      hint: 'A year, into already-taxed accounts.',
      used: (a) => amountPlaceholder(a?.advanced?.annual_taxable_contribution),
    },
    {
      key: 'annualDeferredContribution',
      label: 'Tax-deferred contributions',
      kind: 'money',
      hint: 'A year, pre-tax.',
      used: (a) => amountPlaceholder(a?.advanced?.annual_deferred_contribution),
    },
  ],
  [
    {
      key: 'contributionGrowthPercent',
      label: 'Contribution increase %',
      kind: 'rate',
      hint: "Each year's contributions grow this much.",
      used: (a) => ratePlaceholder(a?.advanced?.contribution_growth),
    },
  ],
  [...LIVING, TARGET],
  [
    {
      key: 'preReturnPercent',
      label: 'Return before %',
      kind: 'rate',
      hint: 'While working.',
      used: (a) => ratePlaceholder(a?.annual_return),
    },
    {
      key: 'postReturnPercent',
      label: 'Return after %',
      kind: 'rate',
      hint: 'In retirement.',
      used: (a) => ratePlaceholder(a?.advanced?.post_retirement_return),
    },
    INFLATION,
  ],
  [
    WITHDRAWAL,
    {
      key: 'returnSpreadPercent',
      label: 'Estimate band ±%',
      kind: 'rate',
      hint: 'How far High and Low sit from the return.',
      used: (a) => ratePlaceholder(a?.return_spread),
    },
  ],
  [
    {
      key: 'preRetirementTaxPercent',
      label: 'Tax rate before %',
      kind: 'rate',
      hint: "Taxes the taxable bucket's growth.",
      used: (a) => ratePlaceholder(a?.pre_retirement_tax_rate),
    },
    {
      key: 'postRetirementTaxPercent',
      label: 'Tax rate after %',
      kind: 'rate',
      hint: 'Taxes deferred withdrawals and taxable growth.',
      used: (a) => ratePlaceholder(a?.post_retirement_tax_rate),
    },
  ],
]

const MODES = [
  ['basic', 'Basic'],
  ['advanced', 'Advanced'],
] as const

function RetirementPlanner() {
  // Two modes, two forms: a balance typed into Advanced must not follow you
  // back to Basic. Only the visible mode's query runs.
  const [mode, setMode] = useState<'basic' | 'advanced'>('basic')
  const [inputs, setInputs] = useState<RetirementInputs>(EMPTY_RETIREMENT_INPUTS)
  const [advancedInputs, setAdvancedInputs] =
    useState<AdvancedRetirementInputs>(EMPTY_ADVANCED_INPUTS)
  const settled = useSettled(inputs)
  const settledAdvanced = useSettled(advancedInputs)
  const basicProjection = useRetirementProjection(settled, mode === 'basic')
  const advancedProjection = useAdvancedRetirementProjection(
    settledAdvanced,
    mode === 'advanced',
  )
  const projection = mode === 'basic' ? basicProjection : advancedProjection
  const assumptions = projection.data?.assumptions

  return (
    <>
      <Tabs value={mode} onValueChange={(value) => setMode(value === 'advanced' ? 'advanced' : 'basic')}>
        <PageHeader
          tabs={
            <TabsList aria-label="Planner mode">
              {MODES.map(([key, label]) => (
                <TabsTrigger key={key} value={key}>
                  {label}
                </TabsTrigger>
              ))}
            </TabsList>
          }
          actions={
            <Button
              size="sm"
              onClick={() =>
                mode === 'basic'
                  ? setInputs(EMPTY_RETIREMENT_INPUTS)
                  : setAdvancedInputs(EMPTY_ADVANCED_INPUTS)
              }
            >
              Reset assumptions
            </Button>
          }
        />
      </Tabs>

      <div className="split">
        <Card
          title="Projected investments"
          subtitle="From the assumptions beside it. Not a market forecast."
        >
          <QueryBoundary query={projection} rows={6}>
            {(data) => <Projection data={data} />}
          </QueryBoundary>
        </Card>

        <Card title="Assumptions" subtitle="Every figure the projection uses.">
          {mode === 'advanced' ? (
            <AssumptionForm
              rows={ADVANCED}
              inputs={advancedInputs}
              settled={settledAdvanced}
              assumptions={assumptions}
              onEdit={(key, value) => setAdvancedInputs((current) => ({ ...current, [key]: value }))}
            />
          ) : (
            <AssumptionForm
              rows={BASIC}
              inputs={inputs}
              settled={settled}
              assumptions={assumptions}
              onEdit={(key, value) => setInputs((current) => ({ ...current, [key]: value }))}
            />
          )}
        </Card>
      </div>
    </>
  )
}

function AssumptionForm<K extends string>({
  rows,
  inputs,
  settled,
  assumptions,
  onEdit,
}: {
  rows: AssumptionRows<K>
  inputs: Record<K, string>
  /** What the projection was asked with; a field error waits for typing to settle. */
  settled: Record<K, string>
  assumptions: RetirementAssumptions | undefined
  onEdit: (key: K, value: string) => void
}) {
  const field = (one: Assumption<K>) => (
    <AssumptionField
      key={one.key}
      assumption={one}
      value={inputs[one.key]}
      error={one.kind === 'money' ? amountFieldError(settled[one.key]) : null}
      assumptions={assumptions}
      onChange={(value) => onEdit(one.key, value)}
    />
  )
  return (
    <div className="stack">
      {rows.map((row) =>
        row.length === 1 ? (
          field(row[0])
        ) : (
          <div key={row[0].key} className="form-row">
            {row.map(field)}
          </div>
        ),
      )}
    </div>
  )
}

function AssumptionField<K extends string>({
  assumption,
  value,
  error,
  assumptions,
  onChange,
}: {
  assumption: Assumption<K>
  value: string
  error: string | null
  assumptions: RetirementAssumptions | undefined
  onChange: (value: string) => void
}) {
  const { label, kind, hint, used } = assumption
  const shown = used(assumptions)
  const edit = (event: ChangeEvent<HTMLInputElement>) => onChange(event.target.value)
  return (
    <Field
      label={label}
      hint={typeof hint === 'function' ? hint(assumptions) : hint}
      error={error}
    >
      {kind === 'money' ? (
        <MoneyInput value={value} placeholder={shown} onChange={edit} />
      ) : (
        <Input
          numeric
          inputMode={kind === 'int' ? 'numeric' : 'decimal'}
          value={value}
          placeholder={shown}
          onChange={edit}
        />
      )}
    </Field>
  )
}

function Projection({ data }: { data: RetirementProjection }) {
  const { assumptions } = data
  const [todaysDollars, setTodaysDollars] = useState(false)
  const retiresIn = assumptions.start_year + data.years_to_retirement
  const axis = data.years.map((year) => ({ key: String(year.year), label: String(year.age) }))

  const series = (pick: (year: RetirementProjection['years'][number]) => MoneyValue) =>
    Object.fromEntries(data.years.map((year) => [String(year.year), moneyToNumber(pick(year))]))

  const markers = [
    {
      at: String(assumptions.start_year + Math.max(data.years_to_retirement, 0)),
      label: 'Retirement',
    },
  ]
  const lastYear = data.years[data.years.length - 1]
  if (lastYear.age > assumptions.retirement_age && lastYear.age === assumptions.life_expectancy) {
    markers.push({ at: String(lastYear.year), label: 'Life expectancy' })
  }

  return (
    <>
      <div className="headline">
        <span className="figure--total">
          <Money value={data.balance_at_retirement} tone="neutral" showCents={false} />
        </span>
        <span className="change__caption">
          at {assumptions.retirement_age} in {retiresIn} —{' '}
          {data.years_to_retirement === 0
            ? 'that age is already behind you'
            : `${plural(data.years_to_retirement, 'year')} away`}
        </span>
        {data.meets_target === null ? null : (
          <Badge tone={data.meets_target ? 'accent' : 'warning'}>
            {data.meets_target ? 'Meets your target' : 'Short of your target'}
          </Badge>
        )}
        {data.runs_out_at_age === null ? null : (
          <Badge tone="warning">Runs out at {data.runs_out_at_age}</Badge>
        )}
      </div>

      <div className="stat-row">
        <MoneyStat
          label="In today's money"
          value={data.balance_at_retirement_in_todays_dollars}
        />
        {assumptions.advanced === null ? (
          <MoneyStat
            label={`Income at ${percent(assumptions.withdrawal_rate)}`}
            value={data.annual_income_in_todays_dollars}
            aside="a year, in today's money"
          />
        ) : null}
        <MoneyStat label="You contribute" value={data.total_contributed} />
        <MoneyStat label="Growth" value={data.total_growth} />
        {data.shortfall === null || data.meets_target ? null : (
          <MoneyStat label="Short by" value={data.shortfall} aside="a year" />
        )}
      </div>

      <BandedProjection
        axis={axis}
        high={series((year) => (todaysDollars ? year.high_balance_in_todays_dollars : year.high_balance))}
        expected={series((year) => (todaysDollars ? year.balance_in_todays_dollars : year.balance))}
        low={series((year) => (todaysDollars ? year.low_balance_in_todays_dollars : year.low_balance))}
        markers={markers}
        height={300}
      />

      <Checkbox
        label="Show inflation-adjusted dollars"
        checked={todaysDollars}
        onCheckedChange={(checked) => setTodaysDollars(checked === true)}
      />

      <p className="muted">
        Age along the bottom. Not a forecast.
        <InfoTip label="How the projection is worked out">
          <p>
            {percent(assumptions.annual_return)} a year, compounded monthly at a twelfth of it and
            discounted by the tax rate of the phase. Contributions land at each month&rsquo;s end
            until retirement; after that, living expenses minus retirement income are drawn each
            month, inflated at {percent(assumptions.annual_inflation)} to the year they land in.
          </p>
          <p>
            High and low use the same arithmetic at ±{percent(assumptions.return_spread)} of
            return.
          </p>
          {assumptions.advanced === null ? null : (
            <p>
              Advanced: the tax-deferred bucket compounds untaxed and pays its tax on the way out.
              The drawdown spends the already-taxed bucket first, then draws grossed-up from the
              deferred one.
            </p>
          )}
        </InfoTip>
      </p>
    </>
  )
}

function MoneyStat({ label, value, aside }: { label: string; value: MoneyValue; aside?: string }) {
  return (
    <Stat label={label}>
      <Money value={value} tone="neutral" showCents={false} />
      {aside ? <span className="stat__aside muted">{aside}</span> : null}
    </Stat>
  )
}

/** An assumption as a percentage, from the fraction the wire carries. */
function percent(rate: WireRate): string {
  return formatPercent(parseRate(rate), { digits: 1 })
}

function placeholder(value: number | undefined): string {
  return value === undefined ? '' : String(value)
}

function amountPlaceholder(amount: MoneyValue | undefined): string {
  return amount === undefined ? '' : amountToWire(amount)
}

function ratePlaceholder(rate: WireRate | undefined): string {
  const parsed = parseRate(rate)
  return parsed === null ? '' : formatPercent(parsed, { digits: 2 }).replace('%', '')
}
