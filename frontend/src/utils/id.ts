// Client-side id generation.
//
// `crypto.randomUUID()` is gated on a **secure context** — HTTPS, or
// localhost. This app is served over plain HTTP on a LAN address
// (`http://192.168.1.202:9150`), which is not a secure context, so on that
// deployment `crypto.randomUUID` is simply `undefined` and calling it
// throws `TypeError: crypto.randomUUID is not a function`.
//
// That is not a cosmetic failure. The one caller is
// `useNoticeStore.push`, so *every* toast in the app — save a tag, run a
// scrape, fail an enqueue — threw before it could render, from inside a
// click handler and in several `catch` blocks. The LAN deployment was
// the one that broke; localhost would have hidden it.
//
// The fallback chain degrades rather than giving up:
//
//   1. `crypto.randomUUID` when present (localhost / HTTPS).
//   2. `crypto.getRandomValues`, which is NOT secure-context gated and is
//      therefore available exactly where (1) is not. Assembled into the
//      same 8-4-4-4-12 shape with the v4 version and variant bits set, so
//      the output is indistinguishable from (1) to any consumer.
//   3. A counter + clock. Not cryptographic, but the only ids using this
//      are React list keys and DOM ids for toasts — uniqueness within the
//      session is the whole requirement.
//
// `globalThis.crypto` is read at call time, not at module load, so a
// late-polyfilled or stubbed crypto is picked up and a missing one is
// survivable rather than a load-time crash.

let seq = 0;

export function newId(): string {
  const c: Crypto | undefined = globalThis.crypto;

  if (c && typeof c.randomUUID === 'function') {
    return c.randomUUID();
  }

  if (c && typeof c.getRandomValues === 'function') {
    const b = c.getRandomValues(new Uint8Array(16));
    // RFC 4122 §4.4: pin the version to 4 and the variant to 10xx, so the
    // value is a well-formed UUID rather than 16 random bytes.
    b[6] = (b[6] & 0x0f) | 0x40;
    b[8] = (b[8] & 0x3f) | 0x80;
    const hex = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
    return [
      hex.slice(0, 8),
      hex.slice(8, 12),
      hex.slice(12, 16),
      hex.slice(16, 20),
      hex.slice(20, 32),
    ].join('-');
  }

  seq += 1;
  return `n${Date.now().toString(36)}-${seq.toString(36)}-${Math.random()
    .toString(36)
    .slice(2, 8)}`;
}
