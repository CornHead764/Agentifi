# The demo media

The README's screenshots and its two-minute tour are the real app rendered against an invented household: Sam
Sample, who banks at Example Bank and Sample Credit Union. Nothing here touches
a server; Playwright answers every `/api` request from `fixtures/`, the same way
`layout/` does, with its own data so the two never move together.

| File | What it is |
| --- | --- |
| `fixtures/` | The household: accounts, half a year of generated history (fixed seed), bills, goals, investments, reports, the assistant's answer and proposals, the Amazon order and Costco receipt |
| `fixtures/state.ts` | What a scene changes as it clicks (a receipt attached, a suggestion accepted), reset for every page |
| `stage.ts` | Signing in, the fixed clock, the drawn pointer, and smooth pointer and scroll moves |
| `scenes.ts` | The ten scenes of the tour and their title cards |
| `screenshots.spec.ts` | The README screenshots, 1440×900 and one phone |
| `video.spec.ts`, `cards.spec.ts` | One recording per scene (1600×900), and the title cards (1920×1080) |
| `assemble.ts` | ffmpeg: quantizes the screenshots and builds the film |

## Regenerating

```sh
cd frontend
npm run demo                      # screenshots, cards and recordings; then docs/screenshots/*.png
node demo/assemble.ts video       # docs/screenshots/demo.mp4, under 10 MB
```

`npm run demo` needs Playwright's Chromium (`npx playwright install chromium`)
and ffmpeg on `PATH` with libx264. Everything it writes besides
`docs/screenshots/` lands in the gitignored `demo/output/`.

GitHub plays a video in a README only from an attachment URL, so the README
embeds a copy of `demo.mp4` uploaded to GitHub rather than the committed file.
After rebuilding the film, drag it into any comment box on GitHub, copy the
`https://github.com/user-attachments/assets/…` URL it produces, and put that
URL on its own line in the README in place of the old one.

Figures shown in the assistant's answer are computed from the fixtures, so they stay true to the
register beside them.
