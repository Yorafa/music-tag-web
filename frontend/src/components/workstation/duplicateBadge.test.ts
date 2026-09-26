// Tests for the two pieces of dedup logic that decide what the user sees
// and what a delete would destroy.
//
// deleteTargetsFor is the one that matters: it is the last thing standing
// between a bulk delete and the user losing the only tagged copy of a song.

import { describe, it, expect } from 'vitest';
import {
  duplicateBadgeSpec,
  deleteTargetsFor,
} from '@/components/workstation/duplicateBadge';
import type { WorklistRow } from '@/types';

function row(id: string, verdict?: WorklistRow['duplicate']): WorklistRow {
  return {
    id,
    fullPath: id,
    fileName: id.split('/').pop() ?? id,
    status: 'pending',
    duplicate: verdict,
  };
}

const dup = (duplicatePath: string) => ({ verdict: 'duplicate' as const, duplicatePath });

describe('duplicateBadgeSpec', () => {
  it('returns null when the row was never checked', () => {
    expect(duplicateBadgeSpec(undefined)).toBeNull();
  });

  // A check that could not conclude must not render as a clean result.
  // "已查重" on a skipped file reads as "no duplicates found", which is the
  // one thing it does not mean.
  it.each(['skipped', 'error'] as const)('renders no badge for %s', (verdict) => {
    expect(duplicateBadgeSpec({ verdict })).toBeNull();
  });

  it('names the other file so the badge is actionable', () => {
    const spec = duplicateBadgeSpec({
      verdict: 'duplicate',
      duplicatePath: 'A/one.mp3',
    });
    expect(spec?.label).toBe('重复');
    expect(spec?.title).toContain('A/one.mp3');
  });

  it('keeps 疑似 visually distinct from 重复', () => {
    const strong = duplicateBadgeSpec({ verdict: 'duplicate', duplicatePath: 'x.mp3' });
    const weak = duplicateBadgeSpec({ verdict: 'likely_duplicate', duplicatePath: 'x.mp3' });
    expect(strong?.label).not.toBe(weak?.label);
    expect(strong?.tone).not.toBe(weak?.tone);
  });
});

describe('deleteTargetsFor', () => {
  it('only deletes rows with a content-level duplicate verdict', () => {
    const rows = [
      row('a.mp3', dup('b.mp3')),
      row('c.mp3', { verdict: 'likely_duplicate', duplicatePath: 'd.mp3' }),
      row('e.mp3', { verdict: 'unique' }),
      row('f.mp3'),
    ];
    // c.mp3 is a NAME clash — deleting it would remove a different
    // recording that happens to share a filename.
    expect(deleteTargetsFor(rows).map((r) => r.id)).toEqual(['a.mp3']);
  });

  it('leaves the keeper out of the delete set', () => {
    const rows = [row('copy.mp3', dup('original.mp3'))];
    const targets = deleteTargetsFor(rows);
    expect(targets.map((r) => r.id)).toEqual(['copy.mp3']);
    expect(targets[0].id).not.toBe('original.mp3');
  });

  it('deletes a copy whose keeper is outside the selection', () => {
    // The keeper was not part of this batch, so it cannot be half of a
    // contradiction and the copy is safe to remove.
    const rows = [row('copy.mp3', dup('unselected/original.mp3'))];
    expect(deleteTargetsFor(rows).map((r) => r.id)).toEqual(['copy.mp3']);
  });

  // The dangerous case. A and B each name the other as the copy to keep,
  // so neither designation can be trusted — deleting either side could
  // remove the only tagged file, and deleting both removes the song.
  it('drops BOTH sides of a mutually-referencing pair', () => {
    const rows = [row('a.mp3', dup('b.mp3')), row('b.mp3', dup('a.mp3'))];
    expect(deleteTargetsFor(rows)).toEqual([]);
  });

  it('still deletes the unambiguous copy in a mixed batch', () => {
    const rows = [
      row('a.mp3', dup('b.mp3')),
      row('b.mp3', dup('a.mp3')),
      row('safe-copy.mp3', dup('safe-original.mp3')),
    ];
    expect(deleteTargetsFor(rows).map((r) => r.id)).toEqual(['safe-copy.mp3']);
  });

  it('handles a three-file chain without deleting everything', () => {
    // c duplicates b, b duplicates a, a duplicates nothing. Only c is
    // unambiguously a copy; b's keeper is itself flagged.
    const rows = [
      row('a.mp3', { verdict: 'unique' }),
      row('b.mp3', dup('a.mp3')),
      row('c.mp3', dup('b.mp3')),
    ];
    const ids = deleteTargetsFor(rows).map((r) => r.id);
    expect(ids).toContain('c.mp3');
    expect(ids).toContain('b.mp3');
  });

  it('returns nothing when no row was checked', () => {
    expect(deleteTargetsFor([row('a.mp3'), row('b.mp3')])).toEqual([]);
  });
});
