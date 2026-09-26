import { describe, expect, it } from 'vitest';
import { skippedNotesFromUpdate } from './skippedNotes';

describe('skippedNotesFromUpdate', () => {
  it('reads the reason the server sent instead of assuming a duplicate', () => {
    // The bug this replaces: every skip rendered as "内容与库内文件完全一致",
    // including rows where nothing was ever compared.
    const res = {
      data: {
        skipped: [
          { file_full_path: 'A/1.ogg', status: 'duplicate', reason: '音频内容指纹（SHA-256）与库内文件完全一致' },
          { file_full_path: 'A/cover.jpg', status: 'not_audio', reason: '找不到文件 "cover.jpg"（请确认该行的 name 是已存在的文件名，而非标题）' },
        ],
      },
    };
    const notes = skippedNotesFromUpdate(res);
    expect(notes.get('A/1.ogg')).toContain('SHA-256');
    expect(notes.get('A/cover.jpg')).toContain('cover.jpg');
    expect(notes.get('A/cover.jpg')).not.toContain('SHA-256');
  });

  it('falls back to status-specific wording when no reason is present', () => {
    const notes = skippedNotesFromUpdate({
      skipped: [
        { file_full_path: 'a.ogg', status: 'duplicate' },
        { file_full_path: 'b.jpg', status: 'not_audio' },
      ],
    });
    expect(notes.get('a.ogg')).toBe('内容与库内文件完全一致');
    expect(notes.get('b.jpg')).toBe('不是可写入标签的音频文件');
  });

  it('still says something useful for an unrecognised status', () => {
    const notes = skippedNotesFromUpdate({
      skipped: [{ file_full_path: 'x.ogg', status: 'brand_new_reason' }],
    });
    expect(notes.get('x.ogg')).toBe('服务端未写入该文件');
  });

  it('treats a blank reason as absent', () => {
    const notes = skippedNotesFromUpdate({
      skipped: [{ file_full_path: 'a.ogg', status: 'duplicate', reason: '   ' }],
    });
    expect(notes.get('a.ogg')).toBe('内容与库内文件完全一致');
  });

  it('returns an empty map for anything that is not a report', () => {
    for (const bad of [null, undefined, 42, 'nope', {}, { data: null }, { skipped: 'x' }]) {
      expect(skippedNotesFromUpdate(bad).size).toBe(0);
    }
  });

  it('drops entries with no usable path rather than keying on undefined', () => {
    const notes = skippedNotesFromUpdate({
      skipped: [{ status: 'duplicate' }, null, 'x', { file_full_path: '', status: 'duplicate' }],
    });
    expect(notes.size).toBe(0);
  });

  it('accepts the unwrapped shape as well as the envelope', () => {
    const wrapped = skippedNotesFromUpdate({ data: { skipped: [{ file_full_path: 'a.ogg' }] } });
    const bare = skippedNotesFromUpdate({ skipped: [{ file_full_path: 'a.ogg' }] });
    expect(bare.get('a.ogg')).toBe(wrapped.get('a.ogg'));
  });
});
