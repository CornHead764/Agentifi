import { useState } from 'react'

import {
  Button,
  Checkbox,
  DialogActions,
  DialogContent,
  Field,
  FormDialog,
  Radio,
  RadioGroup,
  useHeld,
} from '@/components/ui'
import { DEFAULT_RUN_OPTIONS, type Automation, type RunOptions } from '@/lib/clients/automations'

export function RunOptionsDialog({
  automation,
  onRun,
  onClose,
}: {
  automation: Automation | null
  onRun: (automation: Automation, options: RunOptions) => void
  onClose: () => void
}) {
  const shown = useHeld(automation)
  return (
    <FormDialog open={automation !== null} onOpenChange={(open) => !open && onClose()}>
      {shown ? <RunOptionsForm automation={shown} onRun={onRun} /> : null}
    </FormDialog>
  )
}

function RunOptionsForm({
  automation,
  onRun,
}: {
  automation: Automation
  onRun: (automation: Automation, options: RunOptions) => void
}) {
  const [options, setOptions] = useState(DEFAULT_RUN_OPTIONS)
  const perRow = automation.trigger === 'transaction_arrived'

  return (
    <DialogContent
      title={`Run ${automation.name}`}
      description={
        perRow ? 'Against transactions that already arrived and match its trigger.' : 'Once, now.'
      }
      onSubmit={(event) => {
        event.preventDefault()
        onRun(automation, options)
      }}
      footer={
        <DialogActions>
          <Button type="submit" variant="primary">
            {options.dryRun ? 'Dry run' : 'Run for real'}
          </Button>
        </DialogActions>
      }
    >
      {perRow ? (
        <Field label="Against" as="group">
          <RadioGroup
            value={String(options.recent)}
            onValueChange={(value) => setOptions({ ...options, recent: value === '5' ? 5 : 1 })}
          >
            <Radio value="1" label="the most recent matching transaction" />
            <Radio value="5" label="the 5 most recent matching transactions" />
          </RadioGroup>
        </Field>
      ) : null}
      <Checkbox
        label="Change nothing (dry run)"
        checked={options.dryRun}
        onCheckedChange={(checked) => setOptions({ ...options, dryRun: checked === true })}
      />
      {perRow && options.dryRun ? (
        <Checkbox
          label="Hide the current category (blind), to see what it would pick on its own"
          checked={options.blind}
          onCheckedChange={(checked) => setOptions({ ...options, blind: checked === true })}
        />
      ) : null}
    </DialogContent>
  )
}
