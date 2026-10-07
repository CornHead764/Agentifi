import { NotebookPen, Plus, Wand2 } from 'lucide-react'
import { useState } from 'react'
import { Navigate, useNavigate, useParams } from 'react-router-dom'

import { Button, PageHeader, Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui'

import { GuidancePanel } from './rules/GuidancePanel'
import { RulesPanel } from './rules/RulesPanel'


/**
 * Rules and guidance, side by side: a filter says which transactions, and then
 * a rule applies an edit when a row arrives, or a note applies a sentence the
 * next time the assistant looks at one. The tab is the URL (/rules and
 * /rules/guidance), so a link can name the half it means.
 */
type TabKey = 'rules' | 'guidance'

function tabFromPath(segment: string | undefined): TabKey | null {
  if (segment === undefined || segment === '') return 'rules'
  return segment === 'guidance' ? 'guidance' : null
}

function pathForTab(tab: string): string {
  return tab === 'guidance' ? '/rules/guidance' : '/rules'
}

export function RulesPage() {
  const navigate = useNavigate()
  const tab = tabFromPath(useParams().tab)
  const [creating, setCreating] = useState(false)
  if (tab === null) return <Navigate to="/rules" replace />

  return (
    <div className="page page--wide">
      <Tabs value={tab} onValueChange={(next) => navigate(pathForTab(next))}>
        <PageHeader
          tabs={
            <TabsList aria-label="Rules">
              <TabsTrigger value="rules">
                <Wand2 size={14} aria-hidden="true" /> Rules
              </TabsTrigger>
              <TabsTrigger value="guidance">
                <NotebookPen size={14} aria-hidden="true" /> Guidance
              </TabsTrigger>
            </TabsList>
          }
          actions={
            <Button variant="primary" size="sm" onClick={() => setCreating(true)}>
              <Plus size={13} aria-hidden="true" /> {tab === 'guidance' ? 'New guidance' : 'New rule'}
            </Button>
          }
        />
        <TabsContent value="rules">
          <RulesPanel creating={creating} onCreatingChange={setCreating} />
        </TabsContent>
        <TabsContent value="guidance">
          <GuidancePanel creating={creating} onCreatingChange={setCreating} />
        </TabsContent>
      </Tabs>
    </div>
  )
}
