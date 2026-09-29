// Do the three batch dialogs actually REMEMBER their rules?
//
// This file exists because of the lesson from CandidateCard.apply.test.tsx:
// the pure helpers can be perfect and the feature still be broken, because
// the thing that is broken is a dialog forgetting to call them. A test
// that cannot fail is worse than no test, because it reports safety that
// is not there. batchRuleCache.test.ts covers the sanitizers; this covers
// the wiring, by driving the real dialogs — clicking real presets, typing
// in the real boxes, remounting to stand in for a reload.
//
// Each dialog gets the same three questions:
//   1. does a stored rule come back on open?
//   2. does changing the rule write it?
//   3. does a bad stored rule reopen in a USABLE state rather than an
//      error state? (A dialog that opens on a disabled button reads as a
//      broken feature, not as a value that could not be trusted.)

import { describe, it, expect, vi, beforeEach } from 'vitest';

// The rename dialog reaches for the store only to write a row back after a
// rename. Stubbed so mounting the dialog does not start the real store's
// hydrate, whose fire-and-forget writes land in a LATER test (a lesson
// this project has already paid for once).
vi.mock('@/store/useWorklistStore', () => ({
  useWorklistStore: (sel: (s: { renameRow: () => void }) => unknown) =>
    sel({ renameRow: () => {} }),
}));

import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { ParseFilenamesModal } from '@/components/scraper/ParseFilenamesModal';
import { RenameFromTagsDialog } from '@/components/workstation/RenameFromTagsDialog';
import { TidyFolderDialog } from '@/components/workstation/TidyFolderDialog';
import { DEFAULT_TIDY_SEGMENTS } from '@/components/common/batchRuleCache';
import type { WorklistRow } from '@/types';

(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT =
  true;

/** One real row, so the plans actually render instead of the dialogs
 *  showing their empty state. The rules are what is under test; the rows
 *  are only here so the controls around them are live. */
const ROW: WorklistRow = {
  id: '/music/Artist/Album/01 Song.mp3',
  fullPath: '/music/Artist/Album/01 Song.mp3',
  fileName: '01 Song.mp3',
  status: 'pending',
  musicInfo: {
    title: 'Song',
    artist: 'Artist',
    album: 'Album',
    year: '1999',
    tracknumber: '1',
    discnumber: '1',
  },
};

const PARSE_KEY = 'workstation.parsePattern.v1';
const RENAME_KEY = 'workstation.renameTemplate.v1';
const TIDY_ROOT_KEY = 'workstation.tidyRoot.v1';
const TIDY_SEGMENTS_KEY = 'workstation.tidySegments.v1';

/** Mount a dialog, hand back the queries, unmount on `destroy`.
 *
 *  A remount is the closest thing to a reload available here, and it is the
 *  right shape for this feature: the state being tested is initialised in
 *  `useState(load)`, so a fresh mount is exactly what a page reload does. */
async function mount(node: React.ReactNode): Promise<{
  el: (testid: string) => HTMLElement;
  all: (testid: string) => HTMLElement[];
  input: (testid: string) => HTMLInputElement;
  click: (testid: string) => Promise<void>;
  /** Click the Nth match, for controls rendered once per level. */
  clickNth: (testid: string, i: number) => Promise<void>;
  /** The reorder/remove controls carry a title but no testid, and there is
   *  one per level. */
  clickTitle: (title: string, i?: number) => Promise<void>;
  type: (testid: string, value: string) => Promise<void>;
  text: () => string;
  destroy: () => Promise<void>;
}> {
  const host = document.createElement('div');
  document.body.appendChild(host);
  const root: Root = createRoot(host);
  await act(async () => {
    root.render(node);
  });

  // The dialogs portal into document.body, so the queries below search the
  // whole document rather than the host — which is also why each mount
  // cleans up after itself.
  const all = (testid: string) => Array.from(document.querySelectorAll<HTMLElement>(`[data-testid="${testid}"]`));
  const one = (testid: string) => {
    const found = all(testid);
    if (found.length !== 1) {
      throw new Error(`expected exactly one [data-testid="${testid}"], found ${found.length}`);
    }
    return found[0];
  };
  /** The Nth match. The tidy dialog renders its field chips and its
   *  reorder buttons once PER LEVEL, so targeting "the genre chip" means
   *  "the genre chip of level 0" — and saying so is the point. */
  const nth = (testid: string, i: number) => {
    const found = all(testid);
    const node = found[i];
    if (!node) {
      throw new Error(`no [data-testid="${testid}"] at index ${i} (found ${found.length})`);
    }
    return node;
  };
  const byTitle = (title: string, nth = 0) => {
    const found = Array.from(
      document.querySelectorAll<HTMLElement>(`button[title="${title}"]`),
    );
    const node = found[nth];
    if (!node) {
      throw new Error(`no button[title="${title}"] at index ${nth} (found ${found.length})`);
    }
    return node;
  };
  const clickEl = async (node: HTMLElement) => {
    await act(async () => {
      node.click();
    });
  };

  return {
    el: one,
    all,
    input: (testid) => one(testid) as HTMLInputElement,
    click: async (testid) => {
      await clickEl(one(testid));
    },
    clickNth: async (testid, i) => {
      await clickEl(nth(testid, i));
    },
    clickTitle: async (title, i = 0) => {
      await clickEl(byTitle(title, i));
    },
    type: async (testid, value) => {
      const node = one(testid) as HTMLInputElement;
      await act(async () => {
        // React tracks the value it last set, so assigning `.value`
        // directly is invisible to it; go through the native setter and
        // fire the event it actually listens for.
        const setter = Object.getOwnPropertyDescriptor(
          Object.getPrototypeOf(node),
          'value',
        )?.set;
        setter?.call(node, value);
        node.dispatchEvent(new Event('input', { bubbles: true }));
      });
    },
    text: () => document.body.textContent ?? '',
    destroy: async () => {
      await act(async () => {
        root.unmount();
      });
      host.remove();
    },
  };
}

const parseDialog = () => (
  <ParseFilenamesModal open onOpenChange={() => {}} rows={[ROW]} />
);
const renameDialog = () => (
  <RenameFromTagsDialog open onOpenChange={() => {}} rows={[ROW]} />
);
const tidyDialog = () => <TidyFolderDialog open onOpenChange={() => {}} rows={[ROW]} />;

beforeEach(() => {
  window.localStorage.clear();
  document.body.innerHTML = '';
});

// ─── 解析文件名 ─────────────────────────────────────────────────────────────

describe('ParseFilenamesModal remembers its pattern', () => {
  it('opens on the stored pattern', async () => {
    window.localStorage.setItem(PARSE_KEY, '^(?P<artist>.+?) - (?P<title>.+)$');
    const d = await mount(parseDialog());
    try {
      expect(d.input('parse-pattern-input').value).toBe('^(?P<artist>.+?) - (?P<title>.+)$');
    } finally {
      await d.destroy();
    }
  });

  it('writes the pattern when a preset is clicked', async () => {
    const d = await mount(parseDialog());
    try {
      await d.click('parse-preset-artist-title');
      const written = d.input('parse-pattern-input').value;
      expect(written).not.toBe('');
      expect(window.localStorage.getItem(PARSE_KEY)).toBe(written);
    } finally {
      await d.destroy();
    }
  });

  it('writes the pattern when it is typed, not only when a preset is used', async () => {
    // A preset is the easy path; the box is where the real rules get made.
    const d = await mount(parseDialog());
    try {
      await d.type('parse-pattern-input', '^(?P<tracknumber>\\d+) (?P<title>.+)$');
      expect(window.localStorage.getItem(PARSE_KEY)).toBe('^(?P<tracknumber>\\d+) (?P<title>.+)$');
    } finally {
      await d.destroy();
    }
  });

  it('survives a reload: what one session sets, the next one opens with', async () => {
    const first = await mount(parseDialog());
    await first.click('parse-preset-artist-title');
    const written = first.input('parse-pattern-input').value;
    await first.destroy();

    const second = await mount(parseDialog());
    try {
      expect(second.input('parse-pattern-input').value).toBe(written);
    } finally {
      await second.destroy();
    }
  });

  it('reopens a stored pattern it cannot trust on a usable default, not an error', async () => {
    // The failure this guards: a remembered rule that is broken reopens the
    // dialog with 写入 disabled and a red line, which reads as a broken
    // feature rather than as a value that was thrown away.
    window.localStorage.setItem(PARSE_KEY, '^(?P<artist>.+?');
    const d = await mount(parseDialog());
    try {
      expect(d.input('parse-pattern-input').value).toBe('');
      expect(d.all('parse-filenames-pattern-error')).toHaveLength(0);
      expect(d.el('parse-filenames-apply').hasAttribute('disabled')).toBe(false);
    } finally {
      await d.destroy();
    }
  });
});

// ─── 从标签改名 ─────────────────────────────────────────────────────────────

describe('RenameFromTagsDialog remembers its template', () => {
  it('opens on the stored template', async () => {
    window.localStorage.setItem(RENAME_KEY, '${artist} - ${title}');
    const d = await mount(renameDialog());
    try {
      expect(d.input('rename-template-input').value).toBe('${artist} - ${title}');
    } finally {
      await d.destroy();
    }
  });

  it('writes the template when a preset is clicked', async () => {
    const d = await mount(renameDialog());
    try {
      await d.click('rename-preset-artist-title');
      const written = d.input('rename-template-input').value;
      expect(written).not.toBe('');
      expect(window.localStorage.getItem(RENAME_KEY)).toBe(written);
    } finally {
      await d.destroy();
    }
  });

  it('writes the template when a field chip is clicked', async () => {
    // Chips build the template by generating text, so they are a different
    // code path from both the preset and the box.
    const d = await mount(renameDialog());
    try {
      await d.click('rename-field-chip-artist');
      await d.click('rename-field-chip-album');
      expect(window.localStorage.getItem(RENAME_KEY)).toBe('${artist} - ${album}');
    } finally {
      await d.destroy();
    }
  });

  it('survives a reload', async () => {
    const first = await mount(renameDialog());
    await first.click('rename-preset-artist-title');
    const written = first.input('rename-template-input').value;
    await first.destroy();

    const second = await mount(renameDialog());
    try {
      expect(second.input('rename-template-input').value).toBe(written);
    } finally {
      await second.destroy();
    }
  });

  it('reopens a stored template it cannot trust in the neutral state', async () => {
    window.localStorage.setItem(RENAME_KEY, 'no placeholders here');
    const d = await mount(renameDialog());
    try {
      expect(d.input('rename-template-input').value).toBe('');
      expect(d.all('rename-template-error')).toHaveLength(0);
      // Neutral, not broken: the dialog says what to do next instead of
      // showing a complaint about a value the user cannot see.
      expect(d.text()).toContain('先选一条规则');
    } finally {
      await d.destroy();
    }
  });
});

// ─── 整理目录 ───────────────────────────────────────────────────────────────

describe('TidyFolderDialog remembers its destination and its levels', () => {
  it('opens on the stored root and levels', async () => {
    window.localStorage.setItem(TIDY_ROOT_KEY, '/music/Blues');
    window.localStorage.setItem(TIDY_SEGMENTS_KEY, JSON.stringify(['${genre}', '${album}']));
    const d = await mount(tidyDialog());
    try {
      expect(d.input('tidy-root-input').value).toBe('/music/Blues');
      expect(d.all('tidy-level-0').length).toBe(1);
      expect((d.el('tidy-level-0') as HTMLInputElement).value).toBe('${genre}');
      expect((d.el('tidy-level-1') as HTMLInputElement).value).toBe('${album}');
      expect(d.all('tidy-level-2')).toHaveLength(0);
    } finally {
      await d.destroy();
    }
  });

  it('writes the levels when a level is added and filled in', async () => {
    // 加一层 is the path that builds a structure one step at a time — the
    // one a plain preset click never exercises, and the one whose work is
    // most annoying to lose.
    const d = await mount(tidyDialog());
    try {
      await d.click('tidy-add-level');
      await d.type('tidy-level-2', '合辑');
      expect(JSON.parse(window.localStorage.getItem(TIDY_SEGMENTS_KEY) ?? '[]')).toEqual([
        '${artist}',
        '${album}',
        '合辑',
      ]);
    } finally {
      await d.destroy();
    }
  });

  it('does not remember a half-built structure', async () => {
    // addLevel appends an EMPTY level, and an empty level is not a level.
    // Restoring one would reopen the dialog on 「第 3 层：这一层是空的」 with
    // 整理 disabled — the broken-feature look this is all meant to avoid —
    // so the default structure is stored instead. The user's in-progress
    // edit is lost, which is the deal: it was not a rule yet.
    const d = await mount(tidyDialog());
    try {
      await d.click('tidy-add-level');
      expect(window.localStorage.getItem(TIDY_SEGMENTS_KEY)).toBe(
        JSON.stringify([...DEFAULT_TIDY_SEGMENTS]),
      );
    } finally {
      await d.destroy();
    }
  });

  it('writes the levels when a field chip is toggled', async () => {
    // A different route to a new array (toggleField, not setLevel), so a
    // wrapper covering only the text box would be caught here. The chips
    // render per level, so this is level 0's 流派 chip.
    const d = await mount(tidyDialog());
    try {
      await d.clickNth('tidy-field-chip-genre', 0);
      const afterChip = JSON.parse(window.localStorage.getItem(TIDY_SEGMENTS_KEY) ?? '[]');
      expect(afterChip[0]).toContain('genre');
    } finally {
      await d.destroy();
    }
  });

  it('writes the levels when a level is moved', async () => {
    // And a third route (moveLevel). Reordering a structure the user built
    // is exactly the thing that must not come back in the old order. The
    // up button belongs to level 1, so it is the SECOND one.
    const d = await mount(tidyDialog());
    try {
      await d.clickTitle('上移一层', 1);
      const stored = JSON.parse(window.localStorage.getItem(TIDY_SEGMENTS_KEY) ?? '[]');
      expect(stored).toEqual(['${album}', '${artist}']);
    } finally {
      await d.destroy();
    }
  });

  it('writes the levels when one is removed', async () => {
    const d = await mount(tidyDialog());
    try {
      await d.clickTitle('删除这一层', 0);
      expect(JSON.parse(window.localStorage.getItem(TIDY_SEGMENTS_KEY) ?? '[]')).toEqual([
        '${album}',
      ]);
    } finally {
      await d.destroy();
    }
  });

  it('writes the root as it is typed', async () => {
    const d = await mount(tidyDialog());
    try {
      await d.type('tidy-root-input', '/music/Blues');
      expect(window.localStorage.getItem(TIDY_ROOT_KEY)).toBe('/music/Blues');
    } finally {
      await d.destroy();
    }
  });

  it('survives a reload', async () => {
    const first = await mount(tidyDialog());
    await first.click('tidy-preset-genre-artist-year');
    await first.type('tidy-root-input', '/music/Blues');
    const levels = JSON.parse(window.localStorage.getItem(TIDY_SEGMENTS_KEY) ?? '[]');
    await first.destroy();

    const second = await mount(tidyDialog());
    try {
      expect(second.input('tidy-root-input').value).toBe('/music/Blues');
      const restored = ['tidy-level-0', 'tidy-level-1', 'tidy-level-2'].map(
        (id) => (second.el(id) as HTMLInputElement).value,
      );
      expect(restored).toEqual(levels);
    } finally {
      await second.destroy();
    }
  });

  it('reopens a stored level list it cannot trust on the default structure', async () => {
    // One unusable level takes the whole list down — a half-built tree is
    // a wrong answer, not a smaller one.
    window.localStorage.setItem(TIDY_SEGMENTS_KEY, JSON.stringify(['${artist}', '${retired}']));
    const d = await mount(tidyDialog());
    try {
      expect(d.all('tidy-error')).toHaveLength(0);
      expect((d.el('tidy-level-0') as HTMLInputElement).value).toBe(DEFAULT_TIDY_SEGMENTS[0]);
      expect((d.el('tidy-level-1') as HTMLInputElement).value).toBe(DEFAULT_TIDY_SEGMENTS[1]);
    } finally {
      await d.destroy();
    }
  });

  it('starts on the default structure with nothing stored', async () => {
    const d = await mount(tidyDialog());
    try {
      expect(d.input('tidy-root-input').value).toBe('');
      expect((d.el('tidy-level-0') as HTMLInputElement).value).toBe(DEFAULT_TIDY_SEGMENTS[0]);
      expect((d.el('tidy-level-1') as HTMLInputElement).value).toBe(DEFAULT_TIDY_SEGMENTS[1]);
    } finally {
      await d.destroy();
    }
  });
});

// ─── Cross-dialog ───────────────────────────────────────────────────────────

describe('the three rules do not share storage', () => {
  it('editing one leaves the other two alone', async () => {
    // Three separate keys, so a failed or rejected write to one rule cannot
    // take the others down with it — and so 「整理」 cannot clobber the
    // 改名 template just because both are strings in the same dialog family.
    window.localStorage.setItem(PARSE_KEY, '^(?P<artist>.+?) - (?P<title>.+)$');
    window.localStorage.setItem(RENAME_KEY, '${artist} - ${title}');

    const d = await mount(tidyDialog());
    try {
      await d.click('tidy-add-level');
      expect(window.localStorage.getItem(PARSE_KEY)).toBe('^(?P<artist>.+?) - (?P<title>.+)$');
      expect(window.localStorage.getItem(RENAME_KEY)).toBe('${artist} - ${title}');
    } finally {
      await d.destroy();
    }
  });
});
