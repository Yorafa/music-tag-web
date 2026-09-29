import { describe, expect, it } from 'vitest';
import { PREVIEW_LIMIT } from '@/lib/previewLimit';
import {
  localRenamePlan,
  localTidyPlan,
  renderRule,
  splitExt,
  templateVars,
} from './localPreview';
import type { WorklistRow } from '@/types';

const row = (fullPath: string, musicInfo?: Partial<WorklistRow['musicInfo']>): WorklistRow => ({
  id: fullPath,
  fullPath,
  fileName: fullPath.slice(fullPath.lastIndexOf('/') + 1),
  status: 'pending',
  musicInfo,
});

// `title` matters for the rename cases: without it the template renders
// a gap, which is correct behaviour but not what those tests are about.
const TAGS = { artist: '周杰伦', album: '叶惠美', title: '晴天', year: '2003' };

describe('templateVars', () => {
  it('reads the fields a rule may name', () => {
    const v = templateVars({ artist: 'A', album: 'B', year: '2003' });
    expect(v.artist).toBe('A');
    expect(v.year).toBe('2003');
  });

  // musicInfo is Partial and lazily filled, so a row the user never opened
  // has none at all. Absent and empty are the same fact for a rule; making
  // them differ would only invent a disagreement with the server.
  it('treats a missing cache and an empty tag identically', () => {
    expect(templateVars(undefined).artist).toBe('');
    expect(templateVars({}).artist).toBe('');
    expect(templateVars({ artist: '' }).artist).toBe('');
  });

  it('trims, so a padded tag does not produce a padded directory', () => {
    expect(templateVars({ artist: '  A  ' }).artist).toBe('A');
  });
});

describe('renderRule', () => {
  const vars = templateVars(TAGS);

  it('renders fields and fixed text together', () => {
    expect(renderRule('${year} - ${album}', vars).text).toBe('2003 - 叶惠美');
  });

  // Mirrors the server: an empty tag leaves the separator in place, and
  // the dialog is what tells the operator that gap is there.
  it('leaves the separator when a tag is missing', () => {
    const r = renderRule('${genre} - ${album}', vars);
    expect(r.text).toBe('- 叶惠美');
    expect(r.missing).toEqual(['genre']);
  });

  it('reports each missing field once, in rule order', () => {
    const r = renderRule('${genre} - ${albumartist}', vars);
    expect(r.missing).toEqual(['genre', 'albumartist']);
  });

  // A rule that got here with a typo should look wrong, not lose a level.
  it('leaves an unknown placeholder visible', () => {
    expect(renderRule('${albmu}', vars).text).toBe('${albmu}');
  });
});

describe('splitExt', () => {
  it('splits on the last dot', () => {
    expect(splitExt('a.b.flac')).toEqual({ stem: 'a.b', ext: '.flac' });
  });

  // The server's rule: a leading dot is not an extension, so a dotfile
  // keeps its whole name as the stem.
  it('treats a leading dot as part of the name', () => {
    expect(splitExt('.hidden')).toEqual({ stem: '.hidden', ext: '' });
  });

  it('handles no dot at all', () => {
    expect(splitExt('plain')).toEqual({ stem: 'plain', ext: '' });
  });
});

describe('localTidyPlan', () => {
  it('builds root/levels/basename from the row tags', () => {
    const [r] = localTidyPlan([row('Loose/x.mp3', TAGS)], '/lib', ['${artist}', '${album}']);
    expect(r.result).toBe('/lib/周杰伦/叶惠美/x.mp3');
  });

  // Empty root IS the library root server-side, and it is what the dialog
  // leads with. Rendering a leading slash would imply a different place.
  it('treats an empty root as the library root', () => {
    const [r] = localTidyPlan([row('Loose/x.mp3', TAGS)], '', ['${artist}']);
    expect(r.result).toBe('周杰伦/x.mp3');
  });

  it('keeps a level that renders to nothing as 未知, like the server', () => {
    const [r] = localTidyPlan([row('Loose/x.mp3', TAGS)], '', ['${genre}']);
    expect(r.result).toBe('未知/x.mp3');
    expect(r.missing).toEqual(['genre']);
  });

  it('recognises a file already filed this way', () => {
    const [r] = localTidyPlan([row('周杰伦/x.mp3', TAGS)], '', ['${artist}']);
    expect(r.unchanged).toBe(true);
  });

  it('does not call a file unchanged when a subdirectory root was typed', () => {
    // A typed root is a server-side path; the client cannot see into it.
    const [r] = localTidyPlan([row('周杰伦/x.mp3', TAGS)], '/lib', ['${artist}']);
    expect(r.unchanged).toBe(false);
  });

  // Two tracks in one album share a directory ON PURPOSE. Flagging that
  // would put a false conflict on every multi-track release.
  it('does not flag two files in the same album', () => {
    const plan = localTidyPlan(
      [row('Loose/a.mp3', TAGS), row('Loose/b.mp3', TAGS)],
      '',
      ['${artist}', '${album}'],
    );
    expect(plan.every((p) => !p.clash)).toBe(true);
  });

  it('flags two files that would land on the same path', () => {
    const plan = localTidyPlan(
      [row('Loose/x.mp3', TAGS), row('Nested/x.mp3', TAGS)],
      '',
      ['${artist}', '${album}'],
    );
    expect(plan[1].clash).toBe(true);
    expect(plan[0].clash).toBe(false);
  });

  it('previews at most the limit', () => {
    const rows = Array.from({ length: 50 }, (_, i) => row(`Loose/${i}.mp3`, TAGS));
    expect(localTidyPlan(rows, '', ['${artist}'])).toHaveLength(PREVIEW_LIMIT);
  });
});

describe('localRenamePlan', () => {
  it('renders the template and keeps the extension', () => {
    const [r] = localRenamePlan([row('Loose/song.mp3', TAGS)], '${artist} - ${title}');
    expect(r.result).toBe('周杰伦 - 晴天.mp3');
  });

  it('reports a file already carrying that name as unchanged', () => {
    const [r] = localRenamePlan([row('Loose/周杰伦 - 晴天.mp3', TAGS)], '${artist} - ${title}');
    expect(r.unchanged).toBe(true);
  });

  it('flags two files that would get the same name', () => {
    const plan = localRenamePlan(
      [row('One/x.mp3', TAGS), row('Two/x.mp3', TAGS)],
      '${artist} - ${title}',
    );
    expect(plan[1].clash).toBe(true);
  });

  it('previews at most the limit', () => {
    const rows = Array.from({ length: 50 }, (_, i) => row(`Loose/${i}.mp3`, TAGS));
    expect(localRenamePlan(rows, '${artist}')).toHaveLength(PREVIEW_LIMIT);
  });
});
