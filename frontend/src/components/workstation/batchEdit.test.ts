import { describe, it, expect } from 'vitest';
import {
  BATCH_TAG_FIELDS,
  initialBatchForm,
  batchChangedFields,
  buildBatchPayload,
  batchSelectData,
  type BatchFormState,
} from './batchEdit';

/** A form with one field enabled, carrying `value`. */
function formWith(key: string, value: string): BatchFormState {
  const form = initialBatchForm();
  form[key] = { value, skip: false };
  return form;
}

describe('initialBatchForm', () => {
  it('starts with every field skipped, so saving an untouched dialog does nothing', () => {
    const form = initialBatchForm();
    expect(batchChangedFields(form)).toHaveLength(0);
    expect(buildBatchPayload(form).hasChanges).toBe(false);
    expect(buildBatchPayload(form).music_info).toEqual({});
  });

  it('has an entry for every declared field', () => {
    const form = initialBatchForm();
    for (const f of BATCH_TAG_FIELDS) {
      expect(form[f.key]).toEqual({ value: '', skip: true });
    }
  });
});

describe('buildBatchPayload', () => {
  it('omits a skipped field entirely rather than sending it empty', () => {
    // The regression this whole module exists for: submitting "" for an
    // untouched field used to reach the server as "this tag is empty".
    const { music_info } = buildBatchPayload(formWith('genre', 'Ambient'));
    expect(Object.keys(music_info)).toEqual(['genre']);
  });

  it('sends the trimmed value for an enabled field', () => {
    const { music_info } = buildBatchPayload(formWith('artist', '  Ryuichi Sakamoto  '));
    expect(music_info.artist).toBe('Ryuichi Sakamoto');
  });

  it('sends null for an enabled but empty field, which is how a clear is spelled', () => {
    const { music_info } = buildBatchPayload(formWith('genre', ''));
    expect(music_info.genre).toBeNull();
  });

  it('treats a whitespace-only value as a clear, not as a value', () => {
    // A box holding spaces is a value someone typed by accident; writing
    // three spaces into forty files is not what they meant.
    const { music_info } = buildBatchPayload(formWith('comment', '   '));
    expect(music_info.comment).toBeNull();
  });

  it('carries several enabled fields at once', () => {
    const form = initialBatchForm();
    form.title = { value: 'Merry Christmas Mr. Lawrence', skip: false };
    form.album = { value: 'Merry Christmas Mr. Lawrence', skip: false };
    form.genre = { value: '', skip: false };

    const { music_info, fields, hasChanges } = buildBatchPayload(form);
    expect(music_info).toEqual({
      title: 'Merry Christmas Mr. Lawrence',
      album: 'Merry Christmas Mr. Lawrence',
      genre: null,
    });
    expect(fields.map((f) => f.key)).toEqual(['title', 'album', 'genre']);
    expect(hasChanges).toBe(true);
  });

  it('leaves filename out when no rename template is given', () => {
    // filename is a single shared string the server expands per file. Send
    // one literally and every selected file is asked to become that name.
    const { music_info } = buildBatchPayload(formWith('title', 'X'));
    expect('filename' in music_info).toBe(false);
  });

  it('leaves filename out for a template that is only whitespace', () => {
    const { music_info } = buildBatchPayload(formWith('title', 'X'), {
      renameTemplate: '   ',
    });
    expect('filename' in music_info).toBe(false);
  });

  it('sends filename as a template when one is given', () => {
    const { music_info, hasChanges } = buildBatchPayload(formWith('title', 'X'), {
      renameTemplate: '${artist} - ${title}',
    });
    expect(music_info.filename).toBe('${artist} - ${title}');
    expect(hasChanges).toBe(true);
  });

  it('counts a rename on its own as a change, even with no field enabled', () => {
    const { fields, hasChanges, music_info } = buildBatchPayload(initialBatchForm(), {
      renameTemplate: '${title}',
    });
    expect(fields).toHaveLength(0);
    expect(hasChanges).toBe(true);
    expect(music_info.filename).toBe('${title}');
  });

  it('opts out of dedup only when the user turned it off', () => {
    expect(buildBatchPayload(formWith('title', 'X')).music_info.check_duplicate).toBeUndefined();
    expect(
      buildBatchPayload(formWith('title', 'X'), { dedupeEnabled: true }).music_info
        .check_duplicate,
    ).toBeUndefined();
    // The flag has to be inside music_info: a sibling key is dropped by
    // JSON binding without an error.
    expect(
      buildBatchPayload(formWith('title', 'X'), { dedupeEnabled: false }).music_info
        .check_duplicate,
    ).toBe(false);
  });

  it('does not let the dedup flag make an empty form look like a change', () => {
    const { hasChanges, fields } = buildBatchPayload(initialBatchForm(), {
      dedupeEnabled: false,
    });
    expect(hasChanges).toBe(false);
    expect(fields).toHaveLength(0);
  });
});

describe('batchSelectData', () => {
  it('passes relative paths through as row names', () => {
    expect(batchSelectData(['Album/01.mp3', 'Other/02.mp3'])).toEqual([
      { name: 'Album/01.mp3' },
      { name: 'Other/02.mp3' },
    ]);
  });

  it('drops empty paths rather than sending a row the server would skip', () => {
    expect(batchSelectData(['Album/01.mp3', ''])).toEqual([{ name: 'Album/01.mp3' }]);
  });

  it('sends no per-row music_info, which is what makes the shared map apply', () => {
    // A row carrying its own music_info overrides the shared one. Sending
    // one here would make this a per-row scrape payload instead of the
    // uniform edit it is meant to be.
    for (const row of batchSelectData(['a.mp3', 'b/c.mp3'])) {
      expect(Object.keys(row)).toEqual(['name']);
    }
  });
});
