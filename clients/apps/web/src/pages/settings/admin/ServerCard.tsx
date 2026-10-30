import { ExternalLink, RefreshCw } from 'lucide-react'

import { Facts } from '@/components/Facts'
import { QueryBoundary } from '@/components/QueryBoundary'
import { Badge, Button, Card, CopyButton } from '@/components/ui'
import {
  useCheckForUpdate,
  useServerInfo,
  useUpdateStatus,
  type ServerInfo,
  type UpdateStatus,
} from '@/lib/clients/admin'
import { formatTimestamp, timeAgo } from '@agentifi/core/format'

import { describeUpdate, shortCommit } from './serverText'

/** The build that is running, for telling which commit a server is on. */
export function ServerCard() {
  const info = useServerInfo()
  const update = useUpdateStatus()
  const check = useCheckForUpdate()
  return (
    <Card
      title="This server"
      subtitle="The build that is running."
      actions={
        update.data?.enabled ? (
          <Button size="sm" disabled={check.isPending} onClick={() => check.mutate()}>
            <RefreshCw size={13} /> Check for updates
          </Button>
        ) : null
      }
    >
      <QueryBoundary query={info} rows={3}>
        {(server) => (
          <Facts
            facts={[
              { label: 'Version', value: server.version || 'unknown' },
              update.data ? { label: 'Latest release', value: <Latest status={update.data} /> } : null,
              { label: 'Commit', value: <Commit server={server} /> },
              server.built_at ? { label: 'Built', value: formatTimestamp(server.built_at) } : null,
              { label: 'Time zone', value: server.time_zone },
              { label: 'Database schema', value: `Migration ${server.schema_version}` },
            ]}
          />
        )}
      </QueryBoundary>
    </Card>
  )
}

function Commit({ server }: { server: ServerInfo }) {
  if (server.commit === '') {
    return <span title="A development build, made without a commit">unknown</span>
  }
  return (
    <span className="admin-secret">
      <code title={server.commit}>{shortCommit(server.commit)}</code>
      {server.modified ? <Badge tone="warning">Local changes</Badge> : null}
      <CopyButton text={server.commit} variant="ghost" size="sm" />
    </span>
  )
}

function Latest({ status }: { status: UpdateStatus }) {
  const checked = status.checked_at ? `Checked ${timeAgo(status.checked_at, 'long')}` : undefined
  return (
    <span className="admin-secret" title={status.error || checked}>
      {status.update_available ? (
        <Badge tone="accent">{describeUpdate(status)}</Badge>
      ) : (
        <span>{describeUpdate(status)}</span>
      )}
      {status.url ? (
        <a href={status.url} target="_blank" rel="noreferrer noopener" aria-label="Release notes">
          <ExternalLink size={13} aria-hidden="true" />
        </a>
      ) : null}
    </span>
  )
}
