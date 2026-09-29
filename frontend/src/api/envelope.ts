// Unwrapping the gateway's uniform JSON envelope.
//
// Every /api/* handler answers with the same four keys
// (`internal/gateway/handler/response.go`):
//
//     { "result": bool, "code": "200"|"400", "data": …, "message": "…" }
//
// and `Failure()` deliberately returns **HTTP 200** with `result:false` so
// existing callers keep branching on `data.result` rather than on status.
// That makes a failed request indistinguishable from a successful one at
// the axios layer: both resolve.
//
// Most of the client returns the raw envelope and each caller checks
// `res?.result` / `res?.data`. The filename-parse call did NOT — its
// declared return type claimed an unwrapped shape while the body returned
// the envelope verbatim, so `res.results` and `res.token` were
// `undefined` on *every* response, success included. Nothing about the
// call site looked wrong, which is why it survived review: the type
// said `results: ParsedPreviewRow[]` and the code did `return data`.
//
// The result was a guaranteed crash rather than a wrong value. The modal
// did `setResults(undefined)` and then `for (const r of results)` inside
// a `useMemo`; iterating `undefined` throws during render, with no error
// boundary above it, so the whole app went blank. Keeping the unwrap
// behind a function means the next endpoint cannot make the same mistake
// by accident, and a non-iterable payload can never reach a `for…of`.

/** The shape every gateway handler answers with. */
export interface ApiEnvelope<T> {
  result: boolean;
  code: string;
  data: T;
  message: string;
}

/** Return the envelope's `data`, or throw the server's own message.
 *
 *  Throwing (rather than returning a sentinel) matters here: the callers
 *  already wrap these calls in try/catch and surface `err.message`, so a
 *  real backend message — "paths is empty", "save preview: …" — reaches
 *  the user instead of the TypeError that a non-iterable payload
 *  produces a moment later.
 *
 *  `endpoint` is only used to build a message for the two cases the
 *  server did not describe: a non-object body, or `result:false` with no
 *  message. */
export function unwrapEnvelope<T>(body: unknown, endpoint: string): T {
  if (typeof body !== 'object' || body === null) {
    throw new Error(`${endpoint}: 响应格式异常`);
  }
  const env = body as Partial<ApiEnvelope<T>>;
  if (env.result !== true) {
    throw new Error(env.message || `${endpoint} 请求失败`);
  }
  return env.data as T;
}

/** Narrow an unknown value to an array, defaulting to empty.
 *
 *  The last line of defence. `unwrapEnvelope` cannot promise the inside of
 *  `data` is shaped as declared, and a `for…of` over `undefined` is a
 *  render-time throw rather than a wrong value — the one failure mode
 *  worth making structurally impossible at the boundary. */
export function asArray<T>(value: unknown): T[] {
  return Array.isArray(value) ? (value as T[]) : [];
}
