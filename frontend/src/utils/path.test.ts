import { describe, it, expect } from 'vitest';
import { resolveBrowsePath } from '@/utils/path';

describe('resolveBrowsePath', () => {
  // The specific bug: `path || filePath` coerced the empty-string root
  // sentinel into a stale `filePath`, so back-to-root never fetched the
  // root directory. `??` keeps '' intact; only `undefined` triggers
  // the fallback. This case is the regression fence.
  it('returns the empty string when caller passed "" (root sentinel)', () => {
    expect(resolveBrowsePath('', 'lib/old')).toBe('');
  });

  it('returns the fallback when caller passed undefined', () => {
    expect(resolveBrowsePath(undefined, 'lib/old')).toBe('lib/old');
  });

  it('returns the passed value when both are non-empty strings', () => {
    expect(resolveBrowsePath('foo/bar', 'lib/old')).toBe('foo/bar');
  });
});
