import { describe, it, expect } from 'vitest';
import { resolveStreamUrl, metadataOnlyMessage, sourceErrorMessage } from '@/lib/streamUrl';
import type { SourceInfo } from '@/types';
import type { PlayerSource } from '@/store/usePlayerStore';

// Minimal subset of SourceInfo the resolver reads; full type lives in
// @/types and would just bloat this fixture list.
function sourceInfo(name: string, supportsAudioUrl: boolean): SourceInfo {
  return {
    name,
    display_name: name,
    kind: 'tag',
    searchable: true,
    lyric: true,
    supports_id3: true,
    supports_audio_url: supportsAudioUrl,
    default_on: true,
  };
}

const META_KUWO_AUDIO = sourceInfo('kuwo', true);
const META_MUSICBRAINZ = sourceInfo('musicbrainz', false);
const META_YOUTUBE = sourceInfo('youtube', true);
const ALL_SOURCES: SourceInfo[] = [
  META_KUWO_AUDIO,
  META_MUSICBRAINZ,
  META_YOUTUBE,
];

describe('resolveStreamUrl', () => {
  it('returns direct when trackUrl is populated, regardless of source', () => {
    const result = resolveStreamUrl(
      'id-1',
      { kind: 'plugin', source: 'kuwo', songId: 'sid' } as PlayerSource,
      'https://upstream.example/foo.mp3',
      ALL_SOURCES,
    );
    expect(result).toEqual({ kind: 'direct', url: 'https://upstream.example/foo.mp3' });
  });

  it('returns none for metadata-only source when trackUrl is empty', () => {
    const result = resolveStreamUrl(
      'id-2',
      { kind: 'plugin', source: 'musicbrainz', songId: 'mbid' } as PlayerSource,
      '',
      ALL_SOURCES,
    );
    expect(result).toEqual({ kind: 'none', reason: 'metadata-only' });
  });

  it('returns proxy URL for streaming plugin when trackUrl is empty', () => {
    const result = resolveStreamUrl(
      'id-3',
      { kind: 'plugin', source: 'kuwo', songId: 'kuwoSongId' } as PlayerSource,
      '',
      ALL_SOURCES,
    );
    if (result.kind !== 'proxy') {
      throw new Error('expected proxy, got ' + JSON.stringify(result));
    }
    expect(result.url).toBe('/api/stream?src=kuwo&id=kuwoSongId');
  });

  it('returns proxy URL for YouTube source when trackUrl is empty', () => {
    const result = resolveStreamUrl(
      'id-4',
      { kind: 'plugin', source: 'youtube', songId: 'ytVideoId' } as PlayerSource,
      '',
      ALL_SOURCES,
    );
    if (result.kind !== 'proxy') {
      throw new Error('expected proxy, got ' + JSON.stringify(result));
    }
    expect(result.url).toBe('/api/stream?src=youtube&id=ytVideoId');
  });

  it('returns none when source.kind is local and trackUrl is empty', () => {
    const result = resolveStreamUrl(
      'id-5',
      { kind: 'local', fileName: 'foo.mp3', filePath: 'dir/foo.mp3' } as PlayerSource,
      '',
      ALL_SOURCES,
    );
    expect(result).toEqual({ kind: 'none', reason: 'metadata-only' });
  });

  it('returns none when plugin is not in the sources list (defensive)', () => {
    const result = resolveStreamUrl(
      'id-6',
      { kind: 'plugin', source: 'unknown', songId: 'x' } as PlayerSource,
      '',
      [META_KUWO_AUDIO],
    );
    expect(result).toEqual({ kind: 'none', reason: 'metadata-only' });
  });

  it('URL-encodes special characters in src / id', () => {
    const result = resolveStreamUrl(
      'id-7',
      { kind: 'plugin', source: 'ku wo', songId: 'a/b c' } as PlayerSource,
      '',
      [sourceInfo('ku wo', true)],
    );
    if (result.kind !== 'proxy') throw new Error('not proxy');
    // encodeURIComponent escapes spaces too; the slash and space in
    // `a/b c` should round-trip cleanly.
    expect(result.url).toBe('/api/stream?src=ku%20wo&id=a%2Fb%20c');
  });
});

describe('sourceErrorMessage', () => {
  it('returns the per-source Chinese notice when source name is recognised', () => {
    const msg = sourceErrorMessage('kuwo');
    // Don't lock the exact wording — just confirm it isn't the fallback.
    expect(msg).not.toMatch(/^试听链接不可用，请稍后重试或稍后尝试刮擦$/);
    expect(msg.length).toBeGreaterThan(0);
  });

  it('returns a youtube-specific message', () => {
    const msg = sourceErrorMessage('youtube');
    expect(msg).toContain('YouTube');
  });

  it('falls back to a generic message for unknown / undefined source', () => {
    expect(sourceErrorMessage('hypothetical')).toBe(sourceErrorMessage(undefined));
  });
});

describe('metadataOnlyMessage', () => {
  it('returns a stable non-empty string', () => {
    expect(metadataOnlyMessage().length).toBeGreaterThan(0);
  });
});
