// Human-readable byte count.
//
// Extracted because two surfaces needed it and the second copy would be
// the one that drifts: the trash dialog sizes span five orders of
// magnitude, and a directory picker's do not, so the two would disagree on
// exactly the small numbers — the ones a person can still count.

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB'];

export function formatBytes(n: number): string {
  if (!n || n < 0) return '0 B';
  let i = 0;
  let v = n;
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024;
    i += 1;
  }
  // One decimal below 10 keeps "3.4 MB" readable; above it the decimal is
  // noise on a number nobody acts on precisely.
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${UNITS[i]}`;
}
