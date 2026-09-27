import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { buildMediaUrl } from './mediaUrl';

describe('buildMediaUrl', () => {
  it('encodes each segment but keeps the separators', () => {
    expect(buildMediaUrl('foo/中文 bar.flac')).toBe('/media/foo/%E4%B8%AD%E6%96%87%20bar.flac');
  });

  it('handles a file at the library root', () => {
    expect(buildMediaUrl('loose track.mp3')).toBe('/media/loose%20track.mp3');
  });
});

/**
 * `/api/stream/local/?path=…` was hand-written into the worklist row and
 * the detail dialog's play button. No such route exists — the gateway
 * serves local files from the static `/media` mount — so every local
 * preview in those two places 404'd, and the player's HEAD preflight
 * reported the track as unplayable before playback even started. PlayView
 * already used buildMediaUrl and worked, which is why this looked like a
 * cover-image bug rather than a missing endpoint.
 */
describe('local playback URLs', () => {
  const dead = '/api/stream/local/';

  function sources(dir: string, out: string[] = []): string[] {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const p = join(dir, entry.name);
      if (entry.isDirectory()) sources(p, out);
      // Tests are skipped: this file names the dead route in its own
      // comment, and a guard that fails on its own documentation is a
      // guard nobody keeps.
      else if (/\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name)) {
        out.push(p);
      }
    }
    return out;
  }

  it('no component points playback at a route the gateway does not serve', () => {
    const offenders = sources(join(process.cwd(), 'src'))
      .filter((f) => readFileSync(f, 'utf8').includes(dead))
      .map((f) => f.replace(`${process.cwd()}/`, ''));
    expect(offenders).toEqual([]);
  });
});
