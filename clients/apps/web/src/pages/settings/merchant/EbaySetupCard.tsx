import { CollapsibleCard } from '@/components/ui'

const EXEMPTION_NOTE =
  'Self-hosted personal finance application used only by the account holder. It reads the ' +
  "account holder's own purchase history to reconcile their bank transactions. It does not " +
  'store data about any other eBay user (no seller or buyer usernames, IDs, emails or addresses).'

/**
 * How whoever runs this server gets the eBay keys the connector reads through.
 * Keys belong to the installation, never to Agentifi: the OAuth client secret
 * cannot be shipped in an open-source program. Open by default until the
 * server has them.
 */
export function EbaySetupCard({ needsKeys }: { needsKeys: boolean }) {
  return (
    <CollapsibleCard
      title="Setting up eBay"
      subtitle="eBay is read through its own API, with a developer keyset whoever runs this server makes once."
      storageKey={needsKeys ? 'ebay-setup-open' : 'ebay-setup'}
      startCollapsed={!needsKeys}
      what="the setup"
    >
      <ol className="ebay-setup">
        <li>
          <strong>Join eBay's developer program.</strong> At{' '}
          <a href="https://developer.ebay.com" target="_blank" rel="noreferrer noopener">
            developer.ebay.com
          </a>
          , choose <em>Join</em> and sign in with an eBay account; your own is fine. eBay reviews a new
          developer account before it can make keys, which can take about a day.
        </li>
        <li>
          <strong>Make a Production keyset.</strong> Under <em>Application Keys</em>, create an
          application (for example "Agentifi") and its <em>Production</em> keyset. Keep its{' '}
          <em>App ID</em> (the client ID) and <em>Cert ID</em> (the client secret); the Dev ID is not
          needed. The Cert ID is a password for the application: keep it out of chats and screenshots.
        </li>
        <li>
          <strong>Settle eBay's account-deletion notices.</strong> eBay keeps a Production keyset
          switched off until this is done. On <em>Application Keys</em>, click <em>Notifications</em>{' '}
          beside the Production App ID, choose <em>Marketplace Account Deletion</em>, turn on{' '}
          <em>Not persisting eBay data</em>, choose the exemption reason that honestly applies, and
          submit. Agentifi keeps only the account holder's own purchases, never another eBay user's
          details; eBay can penalise an inaccurate answer, so pick the reason yourself. For{' '}
          <em>Additional information</em>, something like:
          <blockquote>{EXEMPTION_NOTE}</blockquote>
        </li>
        <li>
          <strong>Add a redirect name.</strong> Under <em>User Tokens</em>, choose{' '}
          <em>Get a Token from eBay via Your Application</em> for Production and add an{' '}
          <em>eBay Redirect URL</em>. Give it a display title and a privacy policy page; eBay's own
          pages are fine for the accepted and declined URLs. Keep the <em>RuName</em> it shows, which
          looks like <code>Your_Name-YourName-Agenti-abcde</code>.
        </li>
        <li>
          <strong>Give this server the keys.</strong> Whoever runs the server adds to its{' '}
          <code>.env</code>, beside <code>docker-compose.yml</code>:
          <pre>
            {'EBAY_ENVIRONMENT=production\nEBAY_APP_ID=<App ID>\nEBAY_CERT_ID=<Cert ID>\nEBAY_RUNAME=<RuName>'}
          </pre>
          and then runs <code>docker compose up -d</code>. One keyset serves every household on this
          server.
        </li>
        <li>
          <strong>Connect.</strong> Here, <em>Add account</em>, then <em>Open eBay</em>, sign in and
          choose <em>Agree</em>, and paste the address eBay shows afterwards. The first update reads the
          last 90 days, as far back as eBay's API answers; the access lasts 18 months before eBay asks
          again.
        </li>
        <li>
          <strong>Older purchases, once.</strong> The <em>Import orders</em> card below hands over a
          script to run in your own browser on My eBay → Purchases; it saves about three years of
          history as a file to import there.
        </li>
      </ol>
      <p className="hint">
        To try it first, make a <em>Sandbox</em> keyset the same way, set{' '}
        <code>EBAY_ENVIRONMENT=sandbox</code>, and connect with an eBay sandbox test user.
      </p>
    </CollapsibleCard>
  )
}
