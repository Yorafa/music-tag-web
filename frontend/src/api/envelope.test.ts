// Specs for the envelope unwrap.
//
// The regression these exist for was not a wrong value but a crash: the
// filename-parse client returned the raw `{result, code, data, message}`
// envelope while its declared type claimed `{token, results}`, so
// `results` was `undefined` on every response and the modal's
// `for (const r of results)` threw during render with no error boundary
// above it. These assert the shape the callers actually receive.

import { describe, it, expect } from 'vitest';
import { unwrapEnvelope, asArray } from './envelope';

const OK = {
  result: true,
  code: '200',
  message: 'success',
  data: { token: 'abc', results: [{ path: '/m/a.mp3' }] },
};

const FAIL = {
  result: false,
  code: '400',
  message: 'paths is empty',
  data: [],
};

describe('unwrapEnvelope', () => {
  it('returns the inner data on success', () => {
    expect(unwrapEnvelope<typeof OK.data>(OK, 'preview')).toEqual({
      token: 'abc',
      results: [{ path: '/m/a.mp3' }],
    });
  });

  it('does NOT return the envelope itself', () => {
    // The bug: `return data` where `data` is the axios body. This is the
    // assertion that would have caught it.
    const out = unwrapEnvelope<typeof OK.data>(OK, 'preview');
    expect(out).not.toHaveProperty('result');
    expect(out).not.toHaveProperty('code');
    expect(out).not.toHaveProperty('message');
  });

  it('throws the server message on result:false', () => {
    // Failure() answers HTTP 200, so axios resolves and only `result`
    // distinguishes it. It must not be handed back as if it were data.
    expect(() => unwrapEnvelope(FAIL, 'preview')).toThrow('paths is empty');
  });

  it('treats a missing result flag as failure, not success', () => {
    expect(() => unwrapEnvelope({ data: { token: 'x' } }, 'preview')).toThrow();
    expect(() => unwrapEnvelope({ result: false }, 'preview')).toThrow();
  });

  it('names the endpoint when the server gave no message', () => {
    expect(() => unwrapEnvelope({ result: false }, 'preview_parse_filenames')).toThrow(
      /preview_parse_filenames/,
    );
  });

  it('rejects a non-object body with a named error, not a TypeError', () => {
    // A 200 with an HTML error page, or an empty body. Returning
    // `undefined` here is what produced `results === undefined`.
    //
    // The message matters, and a mutation check is what forced it: with
    // the guard removed these inputs still throw, but a raw
    // "Cannot read properties of null" from the property read — which
    // would reach the user as an unexplained TypeError. Asserting only
    // `.toThrow()` passed against the guard's own deletion.
    for (const body of [null, undefined, 'oops', 42, []]) {
      expect(() => unwrapEnvelope(body, 'preview')).toThrow(/preview/);
      expect(() => unwrapEnvelope(body, 'preview')).not.toThrow(TypeError);
    }
  });
});

describe('asArray', () => {
  it('passes a real array through', () => {
    expect(asArray([1, 2])).toEqual([1, 2]);
  });

  it('defaults to empty for anything non-iterable', () => {
    // The exact crash input. `for…of` over any of these throws.
    for (const v of [undefined, null, {}, 'ab', 0, true]) {
      expect(asArray(v)).toEqual([]);
    }
  });

  it('is therefore always safe to iterate', () => {
    // The invariant the modal depends on: a `for…of` can never throw.
    for (const v of [undefined, null, {}, 'ab', [1], 0]) {
      expect(() => {
        let n = 0;
        for (const item of asArray(v)) n += item === undefined ? 0 : 1;
        return n;
      }).not.toThrow();
    }
  });
});
