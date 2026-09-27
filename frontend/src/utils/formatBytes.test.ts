import { describe, it, expect } from 'vitest';
import { formatBytes } from './formatBytes';

describe('formatBytes', () => {
  it('shows raw bytes below 1 KB', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(512)).toBe('512 B');
  });

  it('keeps one decimal while the number is small', () => {
    expect(formatBytes(1024 * 3.4)).toBe('3.4 KB');
  });

  it('drops the decimal once the number is big enough to read', () => {
    expect(formatBytes(1024 * 12)).toBe('12 KB');
    expect(formatBytes(1024 * 1024 * 5)).toBe('5.0 MB');
    expect(formatBytes(1024 * 1024 * 1024 * 2048)).toBe('2.0 TB');
  });

  it('does not report a negative size', () => {
    expect(formatBytes(-1)).toBe('0 B');
  });
});
