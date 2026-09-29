// Tests that the CANDIDATE CARD's apply buttons carry the fetched lyric.
//
// This exists because the first attempt at testing the fix did not work: the
// logic went into a pure `candidateWithLyric` helper, the helper's unit tests
// passed — and then reverting the actual bug (the 应用此标签 button going back
// to `onApply(c)`) left those tests green. They covered the helper, not the
// thing that was broken. A test that cannot fail is worse than no test,
// because it reports safety that is not there.
//
// So this file drives the real component: render it, click the real button,
// and assert on what the parent would have received. There is no
// @testing-library/react in this project, so it goes through react-dom/client
// directly — which is enough, because CandidateCard is presentational and
// its buttons respond to a plain click.

import { describe, it, expect, vi, beforeEach } from 'vitest';

// The card reaches for these only on paths this test does not exercise
// (fetching a lyric), but the module graph still has to resolve.
vi.mock('@/api/lyrics', () => ({ fetchLyricForSong: vi.fn(async () => '') }));

import { createRoot } from 'react-dom/client';
import { act } from 'react';
import { CandidateCard } from '@/components/detail/CandidateCard';
import type { SongInfo } from '@/types';

(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT =
  true;

// The fields CandidateCard actually reads. SongInfo marks several as
// required, so they are spelled out rather than left undefined — the card
// must not be handed a shape the real search response could not produce.
const CANDIDATE: SongInfo = {
  id: '12345',
  name: 'Song Title',
  artist: 'Artist',
  album: 'Album',
  year: '1999',
  source: 'netease',
  artist_id: '',
  album_id: '',
  album_img: '',
};

interface Rendered {
  click: (label: string) => Promise<void>;
  applied: Array<{ candidate: SongInfo; fields?: string[] }>;
  labels: () => string[];
  /** All rendered text, for preview assertions. */
  text: () => string;
  destroy: () => Promise<void>;
}

async function renderCard(
  props: Partial<React.ComponentProps<typeof CandidateCard>> = {},
): Promise<Rendered> {
  const applied: Array<{ candidate: SongInfo; fields?: string[] }> = [];
  const host = document.createElement('div');
  document.body.appendChild(host);
  const root = createRoot(host);

  await act(async () => {
    root.render(
      <CandidateCard
        candidate={CANDIDATE}
        onApply={(c, fields) => applied.push({ candidate: c, fields })}
        cachedLyric={undefined}
        onLyricFetched={() => {}}
        {...props}
      />,
    );
  });

  const buttons = () => Array.from(host.querySelectorAll('button'));
  return {
    labels: () => buttons().map((b) => b.textContent ?? ''),
    text: () => host.textContent ?? '',
    click: async (label: string) => {
      const btn = buttons().find((b) => b.textContent === label);
      if (!btn) {
        throw new Error(
          `no button labelled ${JSON.stringify(label)}; have ${JSON.stringify(
            buttons().map((b) => b.textContent),
          )}`,
        );
      }
      await act(async () => {
        btn.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      });
    },
    applied,
    destroy: async () => {
      await act(async () => root.unmount());
      host.remove();
    },
  };
}

beforeEach(() => {
  document.body.innerHTML = '';
});

describe('CandidateCard apply wiring', () => {
  // The bug: 应用此标签 applied the RAW candidate, whose `lyric` is always
  // empty because a search response never carries one. The lyric the user had
  // fetched and was reading on this very card was silently dropped, and they
  // had to open 详情 and fetch it a second time to get it written.
  it('应用此标签 applies the lyric the card fetched', async () => {
    const card = await renderCard({ cachedLyric: 'line one\nline two' });
    await card.click('应用此标签');
    expect(card.applied).toHaveLength(1);
    expect(card.applied[0].candidate.lyric).toBe('line one\nline two');
    await card.destroy();
  });

  it('应用此标签 still applies every other candidate field', async () => {
    // The lyric override must be narrow: title/artist/album are what the
    // user was matching on and must not be disturbed.
    const card = await renderCard({ cachedLyric: 'lyric text' });
    await card.click('应用此标签');
    const c = card.applied[0].candidate;
    expect(c.name).toBe('Song Title');
    expect(c.artist).toBe('Artist');
    expect(c.album).toBe('Album');
    expect(c.year).toBe('1999');
    await card.destroy();
  });

  it('应用此标签 sends no field list, so the whole candidate is applied', async () => {
    const card = await renderCard({ cachedLyric: 'lyric text' });
    await card.click('应用此标签');
    expect(card.applied[0].fields).toBeUndefined();
    await card.destroy();
  });

  it('应用歌词 applies only the lyric', async () => {
    const card = await renderCard({ cachedLyric: 'lyric text' });
    // The lyric block lives in the collapsed 详情 panel, so the per-field
    // button is not reachable until it is open.
    await card.click('详情');
    await card.click('应用歌词');
    expect(card.applied[0].fields).toEqual(['lyrics']);
    expect(card.applied[0].candidate.lyric).toBe('lyric text');
    await card.destroy();
  });

  it('applies nothing lyric-shaped when no lyric was ever fetched', async () => {
    // No lyric available must leave the field alone, not write ''. The apply
    // path treats an empty value as "this source had nothing" and omits it.
    const card = await renderCard();
    await card.click('应用此标签');
    expect(card.applied[0].candidate.lyric).toBeUndefined();
    await card.destroy();
  });

  it('does not invent a lyric from an empty fetched value', async () => {
    // '' is the parent's record of "this source was asked and had none".
    // Overwriting with it would blank a lyric that was available.
    const card = await renderCard({ cachedLyric: '' });
    await card.click('应用此标签');
    expect(card.applied[0].candidate.lyric).toBeUndefined();
    await card.destroy();
  });

  it('never mutates the candidate object it was given', async () => {
    const card = await renderCard({ cachedLyric: 'lyric text' });
    await card.click('应用此标签');
    // A shared candidate array (the parent's `candidates` state) mutated here
    // would make the card's own preview change under the user's feet.
    expect(CANDIDATE.lyric).toBeUndefined();
    await card.destroy();
  });

  it('shows the fetched lyric in the preview', async () => {
    const card = await renderCard({ cachedLyric: 'preview me' });
    await card.click('详情');
    // The preview is a <pre> holding the lyric. If the card shows a lyric it
    // cannot be applying it — that mismatch WAS the bug, so the preview and
    // the apply are asserted together here on purpose.
    expect(card.text()).toContain('preview me');
    await card.destroy();
  });

  it('offers 获取歌词 and not 应用歌词 before anything is fetched', async () => {
    // The button the user presses is the one that decides whether the apply
    // path can carry a lyric at all, so its presence is the regression signal.
    const card = await renderCard();
    await card.click('详情');
    expect(card.labels()).toContain('获取歌词');
    expect(card.labels()).not.toContain('应用歌词');
    await card.destroy();
  });
});
