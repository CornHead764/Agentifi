/**
 * Turns what `npm run demo` recorded into the published media, with ffmpeg:
 *
 *   node demo/assemble.ts stills            the README screenshots, quantized, into docs/screenshots/
 *   node demo/assemble.ts video [out.mp4]   the film: title cards between the scenes, 1080p H.264,
 *                                           into docs/screenshots/demo.mp4
 *   node demo/assemble.ts all [out.mp4]     both
 *
 * `npm run demo` runs `stills` itself; the film is built by hand.
 */
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { basename, dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const OUTPUT = join(HERE, 'output')
const DOCS = join(HERE, '..', '..', 'docs', 'screenshots')
const CLIPS = join(OUTPUT, 'clips')

const FPS = 30
const ENCODE = ['-c:v', 'libx264', '-preset', 'slow', '-crf', '18', '-pix_fmt', 'yuv420p', '-r', String(FPS)]
const CARD_SECONDS = 1.9
const BOOKEND_SECONDS = 3.0
/** Scenes play a touch faster than they were clicked, to keep the film under two minutes. */
const PACE = 1.06
/** GitHub plays a README video only from an attachment, and caps a free account's at 10 MB. */
const FILM_BYTES = 9_000_000

function ffmpeg(...args: string[]): void {
  execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', ...args], { stdio: 'inherit' })
}

function kb(path: string): string {
  return `${Math.round(statSync(path).size / 1024)} KB`
}

function stills(): void {
  const from = join(OUTPUT, 'screenshots')
  mkdirSync(DOCS, { recursive: true })
  for (const name of readdirSync(from).filter((one) => one.endsWith('.png'))) {
    const target = join(DOCS, name)
    // A 256-colour palette built from the image itself: flat UI colours come
    // through exactly, and no dither speckles the text.
    ffmpeg('-i', join(from, name), '-vf', 'split[a][b];[a]palettegen=max_colors=256:stats_mode=full[p];[b][p]paletteuse=dither=none', target)
    console.log(`${name}: ${kb(join(from, name))} -> ${kb(target)}`)
  }
}

function sceneClips(): string[] {
  const dir = join(OUTPUT, 'video')
  return readdirSync(dir)
    .filter((one) => one.endsWith('.webm'))
    .sort()
    .map((one) => basename(one, '.webm'))
    .filter((stem) => existsSync(join(dir, `${stem}.json`)))
}

function cardClip(name: string, seconds: number): string {
  const out = join(CLIPS, `card-${name}.mp4`)
  const fadeOut = (seconds - 0.35).toFixed(2)
  ffmpeg('-loop', '1', '-t', String(seconds), '-i', join(OUTPUT, 'cards', `${name}.png`), '-vf', `fps=${FPS},format=yuv420p,fade=in:st=0:d=0.35,fade=out:st=${fadeOut}:d=0.35`, ...ENCODE, out)
  return out
}

function sceneClip(stem: string): string {
  const meta = JSON.parse(readFileSync(join(OUTPUT, 'video', `${stem}.json`), 'utf8'))
  const length = Number(meta.length)
  const out = join(CLIPS, `scene-${stem}.mp4`)
  const fadeOut = (length / PACE - 0.3).toFixed(2)
  ffmpeg(
    '-ss', String(meta.start), '-i', join(OUTPUT, 'video', `${stem}.webm`), '-t', String(length),
    '-vf', `setpts=PTS/${PACE},scale=1920:1080:flags=lanczos,fps=${FPS},fade=in:st=0:d=0.25,fade=out:st=${fadeOut}:d=0.3`,
    ...ENCODE, out,
  )
  return out
}

function video(out: string): void {
  rmSync(CLIPS, { recursive: true, force: true })
  mkdirSync(CLIPS, { recursive: true })
  const scenes = sceneClips()
  const parts = [cardClip('00-open', BOOKEND_SECONDS)]
  for (const stem of scenes) {
    parts.push(cardClip(stem, CARD_SECONDS), sceneClip(stem))
  }
  parts.push(cardClip(`${String(scenes.length + 1).padStart(2, '0')}-close`, BOOKEND_SECONDS + 0.6))
  const list = join(CLIPS, 'list.txt')
  writeFileSync(list, parts.map((part) => `file '${part}'`).join('\n'))
  const joined = join(CLIPS, 'film.mp4')
  ffmpeg('-f', 'concat', '-safe', '0', '-i', list, '-c', 'copy', joined)
  const seconds = Number(execFileSync('ffprobe', ['-v', 'error', '-show_entries', 'format=duration', '-of', 'csv=p=0', joined]).toString().trim())
  // Two passes at the bitrate that fills FILM_BYTES, so the size is met
  // whatever the film's length.
  const bitrate = `${Math.floor((FILM_BYTES * 8) / seconds / 1000)}k`
  const twoPass = ['-c:v', 'libx264', '-preset', 'veryslow', '-b:v', bitrate, '-passlogfile', join(CLIPS, 'pass'), '-an']
  ffmpeg('-i', joined, ...twoPass, '-pass', '1', '-f', 'mp4', '/dev/null')
  mkdirSync(dirname(out), { recursive: true })
  ffmpeg('-i', joined, ...twoPass, '-pass', '2', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', '-map_metadata', '-1', out)
  console.log(`${out}: ${seconds.toFixed(1)} s, ${kb(out)}`)
}

const [step = 'stills', target = join(DOCS, 'demo.mp4')] = process.argv.slice(2)
try {
  execFileSync('ffmpeg', ['-version'], { stdio: 'ignore' })
} catch {
  console.error('ffmpeg is not on PATH; install it (or point PATH at a static build) and run this again.')
  process.exit(1)
}
if (step === 'stills' || step === 'all') stills()
if (step === 'video' || step === 'all') video(target)
