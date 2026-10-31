import { Navigate, useNavigate, useParams } from 'react-router-dom'

import { PageHeader, Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui'
import { ALL_MERCHANTS, MERCHANTS, readMerchantId } from '@/lib/merchants'

import { MerchantSettings } from './MerchantSettings'

/**
 * The Merchants section: one screen per merchant behind a picker built from
 * `MERCHANTS`. The shop is in the URL, so a reload lands on the same one.
 */
export function MerchantsSettings() {
  const navigate = useNavigate()
  const merchant = readMerchantId(useParams().merchant)
  // An unknown or absent merchant lands on the first one rather than on an
  // empty section: /settings/merchants is a bookmark somebody will make.
  if (merchant === null) return <Navigate to={MERCHANTS[ALL_MERCHANTS[0]].settingsPath} replace />

  return (
    <Tabs
      value={merchant}
      onValueChange={(next) => {
        const chosen = readMerchantId(next)
        if (chosen !== null) navigate(MERCHANTS[chosen].settingsPath)
      }}
    >
      <PageHeader
        title="Merchants"
        tabs={
          <TabsList aria-label="Merchant">
            {ALL_MERCHANTS.map((id) => (
              <TabsTrigger key={id} value={id}>
                {MERCHANTS[id].name}
              </TabsTrigger>
            ))}
          </TabsList>
        }
      />
      {ALL_MERCHANTS.map((id) => (
        <TabsContent key={id} value={id}>
          <MerchantSettings merchant={id} />
        </TabsContent>
      ))}
    </Tabs>
  )
}
