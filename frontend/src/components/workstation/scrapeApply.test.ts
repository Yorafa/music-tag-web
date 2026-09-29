// Tests for the two scrape bugs fixed alongside the download-naming change.
//
// Both were invisible failures: no error, no wrong-looking state, just an
// outcome the user had to work around by hand. That is why the logic behind
// each was pulled out of the component into a pure function — there is no
// @testing-library/react here, so inline logic is untestable by construction.

import { describe, it, expect } from 'vitest';
import {
  resolveCandidateSearchQuery,
  effectiveCandidateLyric,
  candidateWithLyric,
  type ScrapeCandidate,
} from './scrapedInfo';

describe('resolveCandidateSearchQuery', () => {
  it('uses the CURRENT title, not the one the dialog opened with', () => {
    // The bug: searchQuery was seeded from the title once and then preferred
    // over the live title forever, so editing the title had no effect on the
    // search. Here the only title offered IS the edited one, and it is used.
    expect(
      resolveCandidateSearchQuery({ title: '新标题', fileName: 'old.flac' }),
    ).toBe('新标题');
  });

  it('prefers an explicit query from the caller above everything', () => {
    // The fingerprint search passes a path, not a title; that must win.
    expect(
      resolveCandidateSearchQuery({
        explicit: 'Music/a/b.flac',
        override: 'typed',
        title: 'Some Title',
      }),
    ).toBe('Music/a/b.flac');
  });

  it('uses the search box when the user typed one', () => {
    expect(
      resolveCandidateSearchQuery({ override: 'typed', title: 'Some Title' }),
    ).toBe('typed');
  });

  it('falls back to the filename stem when there is no title', () => {
    expect(resolveCandidateSearchQuery({ fileName: 'Song Name.flac' })).toBe(
      'Song Name',
    );
    // No extension: a dotted filename still loses only its last segment.
    expect(resolveCandidateSearchQuery({ fileName: 'a.b.c' })).toBe('a.b');
  });

  it('returns undefined rather than searching for nothing', () => {
    // An empty query sent to a source returns "no matches", which reads as
    // "this song is not in any source" — a wrong claim, not a neutral one.
    expect(resolveCandidateSearchQuery({})).toBeUndefined();
    expect(resolveCandidateSearchQuery({ title: '' })).toBeUndefined();
    expect(
      resolveCandidateSearchQuery({ title: '   ', fileName: '' }),
    ).toBeUndefined();
  });

  it('treats a whitespace-only title as absent, not as the query', () => {
    // " " would be sent verbatim and match nothing, while looking like the
    // user had typed something.
    expect(
      resolveCandidateSearchQuery({ title: '   ', fileName: 'Real.flac' }),
    ).toBe('Real');
  });

  it('trims the result', () => {
    expect(resolveCandidateSearchQuery({ title: '  Title  ' })).toBe('Title');
  });

  it('skips an empty override rather than falling through to a blank search', () => {
    expect(
      resolveCandidateSearchQuery({ override: '', title: 'Title' }),
    ).toBe('Title');
  });
});

describe('effectiveCandidateLyric', () => {
  const candidate: ScrapeCandidate = { name: 'T', artist: 'A' };

  it('is undefined when nothing has been fetched', () => {
    // A search never carries lyrics, so this is the state every card starts
    // in. undefined (not '') is load-bearing: it distinguishes "not fetched"
    // from "fetched, source had none".
    expect(effectiveCandidateLyric(candidate)).toBeUndefined();
    expect(effectiveCandidateLyric(candidate, undefined)).toBeUndefined();
  });

  it('prefers the fetched lyric', () => {
    expect(effectiveCandidateLyric(candidate, 'fetched')).toBe('fetched');
  });

  it('falls back to whatever the candidate carried', () => {
    expect(effectiveCandidateLyric({ ...candidate, lyric: 'c' })).toBe('c');
    expect(effectiveCandidateLyric({ ...candidate, lyrics: 'c2' })).toBe('c2');
  });

  it('treats an empty fetched lyric as "source had none"', () => {
    // The parent stores '' for a source that was asked and had no lyric. That
    // must not silently fall back to some other source's text.
    expect(effectiveCandidateLyric(candidate, '')).toBeUndefined();
  });
});

describe('candidateWithLyric', () => {
  const candidate: ScrapeCandidate = {
    name: 'Song',
    artist: 'Artist',
    album: 'Album',
    year: '1999',
    genre: 'Rock',
  };

  it('folds a fetched lyric into the candidate', () => {
    const out = candidateWithLyric(candidate, 'la la la');
    expect(out.lyric).toBe('la la la');
  });

  it('leaves every other field untouched', () => {
    // Narrow on purpose: a card must not be able to "apply" something the
    // user did not choose.
    const out = candidateWithLyric(candidate, 'la la la');
    expect(out.name).toBe('Song');
    expect(out.artist).toBe('Artist');
    expect(out.album).toBe('Album');
    expect(out.year).toBe('1999');
    expect(out.genre).toBe('Rock');
  });

  it('returns the candidate unchanged when there is no lyric', () => {
    // Identity, not a copy: the "leave my tag alone" rule for lyrics means
    // no lyric field must be introduced as an empty string.
    expect(candidateWithLyric(candidate)).toBe(candidate);
    expect(candidateWithLyric(candidate, '')).toBe(candidate);
  });

  it('does not overwrite a candidate lyric with an empty fetched one', () => {
    // '' means "this source was asked and had no lyric". If that overwrote
    // the candidate's own text, the apply would blank a lyric that was
    // actually available.
    const withLyric = { ...candidate, lyric: 'from search' };
    expect(candidateWithLyric(withLyric, '').lyric).toBe('from search');
    expect(candidateWithLyric(withLyric, 'fetched').lyric).toBe('fetched');
  });

  it('does not mutate its input', () => {
    const input = { ...candidate };
    candidateWithLyric(input, 'new');
    expect(input.lyric).toBeUndefined();
  });
});
