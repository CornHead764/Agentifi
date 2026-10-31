import {
  Button,
  CopyableSecret,
  Dialog,
  DialogActions,
  DialogContent,
  useHeld,
} from '@/components/ui'

/** The one sight of a minted password: nothing reads it back. */
export function MintedPasswordDialog({
  minted: openMinted,
  onClose,
}: {
  minted: { email: string; password: string } | null
  onClose: () => void
}) {
  const minted = useHeld(openMinted)

  return (
    <Dialog
      open={openMinted !== null}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      {minted !== null ? (
        <DialogContent
          title="Copy this password now"
          description={`Shown only once. If lost, set a new one for ${minted.email}.`}
          footer={
            <DialogActions cancel={false}>
              <Button variant="primary" onClick={onClose}>
                Done
              </Button>
            </DialogActions>
          }
        >
          <CopyableSecret value={minted.password} />
        </DialogContent>
      ) : null}
    </Dialog>
  )
}
