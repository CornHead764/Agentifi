import { Callout, Dialog, DialogContent, Spinner } from '@/components/ui'
import { useBlobUrl } from '@/lib/useBlobUrl'

import {
  showFailureScreenshot,
  useShownFailureScreenshot,
  type FailureScreenshot,
} from './failure-screenshot'

/** Mounted once, by the shell. */
export function FailureScreenshotDialog() {
  const shot = useShownFailureScreenshot()
  return (
    <Dialog
      open={shot !== null}
      onOpenChange={(open) => {
        if (!open) showFailureScreenshot(null)
      }}
    >
      {shot === null ? null : 'path' in shot ? (
        <KeptScreenshot key={shot.path} path={shot.path} provider={shot.provider} />
      ) : (
        <ScreenshotContent provider={shot.provider} url={`data:image/png;base64,${shot.image}`} />
      )}
    </Dialog>
  )
}

function KeptScreenshot({ path, provider }: { path: string; provider: string }) {
  const { url, failed } = useBlobUrl(path)
  return <ScreenshotContent provider={provider} url={url} failed={failed} />
}

function ScreenshotContent({
  provider,
  url,
  failed = false,
}: {
  provider: string
  url: string | null
  failed?: boolean
}) {
  return (
    <DialogContent
      wide
      title={`The page ${provider} showed`}
      description="What the server's browser saw when it stopped. Anything typed into the page is blanked out."
    >
      {url !== null ? (
        <img
          className="failure-screenshot"
          src={url}
          alt={`The page ${provider} showed when it stopped`}
        />
      ) : failed ? (
        <Callout tone="warning">
          This screenshot has gone: an update since has got in, or a newer one stopped without a
          page.
        </Callout>
      ) : (
        <Spinner size={18} />
      )}
    </DialogContent>
  )
}

/** The link beside a connector's failure, for a failure that left a page behind. */
export function FailureScreenshotLink(shot: FailureScreenshot) {
  return (
    <button type="button" className="link" onClick={() => showFailureScreenshot(shot)}>
      Show screenshot
    </button>
  )
}
