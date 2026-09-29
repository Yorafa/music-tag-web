import { describe, expect, it } from 'vitest';
import {
  PREVIEW_LIMIT,
  capPreview,
  previewTruncationNote,
  previewTruncationNoteBound,
} from './previewLimit';

describe('capPreview', () => {
  it('leaves a small selection alone', () => {
    expect(capPreview([1, 2, 3])).toEqual([1, 2, 3]);
  });

  it('takes exactly the limit', () => {
    const many = Array.from({ length: 500 }, (_, i) => i);
    expect(capPreview(many)).toHaveLength(PREVIEW_LIMIT);
    expect(capPreview(many)[0]).toBe(0);
  });

  it('handles an empty selection', () => {
    expect(capPreview([])).toEqual([]);
  });

  // The first rows, not a sample: the operator recognises the shape of
  // their rule from the top of their own selection, which is the order
  // they would see anyway.
  it('keeps the head of the list', () => {
    expect(capPreview(['a', 'b', 'c', 'd', 'e'])).toEqual(['a', 'b', 'c', 'd', 'e']);
  });
});

describe('previewTruncationNote', () => {
  it('says nothing when nothing was cut', () => {
    expect(previewTruncationNote(3, 3)).toBeNull();
  });

  // Both numbers, always. "只预览了 10 个" alone reads as a bug, and
  // "预览了 10 个" alone hides that the rest were never looked at.
  it('carries both the previewed count and the total', () => {
    const s = previewTruncationNote(10, 200)!;
    expect(s).toContain('10');
    expect(s).toContain('200');
  });

  // The point of the note: a capped plan is NOT a smaller job.
  it('makes clear the apply still covers everything', () => {
    expect(previewTruncationNote(10, 200)).toContain('全部 200 个');
  });
});

describe('previewTruncationNoteBound', () => {
  it('says nothing when nothing was cut', () => {
    expect(previewTruncationNoteBound(10, 10)).toBeNull();
  });

  // 解析文件名's apply is bound to the preview's token, so its note has
  // to say the WRITE is unaffected — the operator is about to change
  // files, not just look at them.
  it('makes clear the write still covers everything', () => {
    expect(previewTruncationNoteBound(10, 300)).toContain('写入全部 300 个');
  });
});
