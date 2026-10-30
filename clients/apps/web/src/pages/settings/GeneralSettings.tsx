import { PageHeader } from '@/components/ui'

import { DisplayPreferencesCard } from './DisplayPreferencesCard'
import { PersonalDetailsCard } from './PersonalDetailsCard'

export function GeneralSettings() {
  return (
    <>
      <PageHeader title="General" />

      <DisplayPreferencesCard />

      <PersonalDetailsCard />
    </>
  )
}
