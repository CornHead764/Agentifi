import { Facts } from '@/components/Facts'
import { QueryBoundary } from '@/components/QueryBoundary'
import { Badge, Card, CopyButton } from '@/components/ui'
import { useServerInfo, type ServerInfo } from '@/lib/clients/admin'
import { formatTimestamp } from '@/lib/format'

import { shortCommit } from './serverText'

/** The build that is running, for telling which commit a server is on. */
export function ServerCard() {
  const info = useServerInfo()
  return (
    <Card title="This server" subtitle="The build that is running.">
      <QueryBoundary query={info} rows={3}>
        {(server) => (
          <Facts
            facts={[
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
