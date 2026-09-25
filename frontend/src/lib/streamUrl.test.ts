import { describe, it, expect, vi } from 'vitest';
import {
  resolveStreamUrl,
  resolveDownloadUrl,
  audioDownloadBasename,
  metadataOnlyMessage,
  sourceErrorMessage,
} from '@/lib/streamUrl';
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
    // Trailing slash before `?` mirrors the route registration
    // (`authed.GET("/stream/", handler.StreamAudio)` in router.go) so
    // gin's RedirectTrailingSlash default doesn't fire a 301 that the
    // native <audio> element would have to chase with a second GET.
    expect(result.url).toBe('/api/stream/?src=kuwo&id=kuwoSongId');
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
    // Same trailing-slash rationale as the kuwo test above.
    expect(result.url).toBe('/api/stream/?src=youtube&id=ytVideoId');
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
    expect(result.url).toBe('/api/stream/?src=ku%20wo&id=a%2Fb%20c');
  });
});

describe('resolveDownloadUrl', () => {
  it('returns the as_attachment URL with trailing slash', () => {
    const result = resolveDownloadUrl(
      { kind: 'plugin', source: 'kuwo', songId: 'kuwoSongId' } as PlayerSource,
      '',
      ALL_SOURCES,
    );
    expect(result).toBe('/api/stream/?src=kuwo&id=kuwoSongId&as_attachment=1');
  });

  it('appends filename query when provided', () => {
    const result = resolveDownloadUrl(
      { kind: 'plugin', source: 'youtube', songId: 'abc' } as PlayerSource,
      '',
      ALL_SOURCES,
      'Artist - Title.ogg',
    );
    expect(result).toBe(
      '/api/stream/?src=youtube&id=abc&as_attachment=1&filename=Artist%20-%20Title.ogg',
    );
  });
});

describe('audioDownloadBasename', () => {
  it('builds artist - title.ogg', () => {
    expect(audioDownloadBasename('Ado', 'ウタ', 'ogg')).toBe('Ado - ウタ.ogg');
  });

  it('sanitizes unsafe path chars', () => {
    expect(audioDownloadBasename('A/B', 'C:D', 'ogg')).toBe('A_B - C_D.ogg');
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

describe('waitForStreamReady', () => {
  it('resolves on 200/206 after a 202 pending round', async () => {
    const { waitForStreamReady } = await import('@/lib/streamUrl');
    const fetchMock = vi
      .fn()
      // first: still downloading (HEAD)
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ result: false, code: 'download_pending', message: '下载中' }), {
          status: 202,
          headers: {
            'Content-Type': 'application/json; charset=utf-8',
            'Retry-After': '0',
            'X-Download-Retry-Budget': '3',
          },
        }),
      )
      // second: warm cache (HEAD → 200 is enough; 206 only for Range)
      .mockResolvedValueOnce(
        new Response(null, {
          status: 200,
          headers: { 'Content-Type': 'audio/mpeg', 'Content-Length': '12' },
        }),
      );
    vi.stubGlobal('fetch', fetchMock);
    await expect(waitForStreamReady('/api/stream/?src=youtube&id=abc')).resolves.toBeUndefined();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock.mock.calls[0][1]?.method).toBe('HEAD');
    vi.unstubAllGlobals();
  });

  it('treats 416 as pending then resolves', async () => {
    const { waitForStreamReady } = await import('@/lib/streamUrl');
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(null, { status: 416 }))
      .mockResolvedValueOnce(
        new Response(null, {
          status: 200,
          headers: { 'Content-Type': 'audio/mpeg', 'Content-Length': '12' },
        }),
      );
    vi.stubGlobal('fetch', fetchMock);
    await expect(waitForStreamReady('/api/stream/?src=youtube&id=abc')).resolves.toBeUndefined();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    vi.unstubAllGlobals();
  });

  it('throws UTF-8 decoded message on hard failure', async () => {
    const { waitForStreamReady } = await import('@/lib/streamUrl');
    const msg = 'download 异步入队失败';
    const body = new TextEncoder().encode(
      JSON.stringify({ result: false, code: '400', message: msg }),
    );
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(body, {
          status: 503,
          headers: { 'Content-Type': 'application/json; charset=utf-8' },
        }),
      ),
    );
    await expect(waitForStreamReady('/api/stream/?src=youtube&id=abc')).rejects.toThrow(msg);
    vi.unstubAllGlobals();
  });
});
