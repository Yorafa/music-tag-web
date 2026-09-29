// One row's 32-pixel cover thumbnail.
//
// WHY THIS IS ITS OWN COMPONENT. The worklist used to receive its cover
// inline, as a base64 data URI on the same music_id3 response that carried
// the title and artist. That is fine for one file and ruinous for five
// hundred: an embedded cover is a full-resolution scan, and a real library
// returns 3–15 MB per response, so hydrating a 500-row queue transferred
// gigabytes to paint thumbnails 32 pixels wide. The batch read now asks for
// tags WITHOUT artwork, and this fetches the image separately — only for
// rows that are actually on screen.
//
// "Actually on screen" is nearly free here because the table is virtualized:
// ~13 rows are mounted at a time, so at most ~13 covers are in flight
// regardless of whether the queue holds 50 tracks or 50 000. Scrolling
// away unmounts the row, and the browser's HTTP cache (the endpoint sends
// Cache-Control plus an mtime-based ETag) means scrolling back does not
// re-download.
//
// The gradient placeholder is not a loading spinner. A cover-less file is a
// normal state — a ripped single, a B-side — and the server answers 404 for
// it, so the gradient IS the final state for those rows. It is also what
// shows while the bytes are in flight, which is why nothing here animates: a
// flash on every scroll would read as flicker.
//
// THE LOAD STATE IS KEYED BY PATH, NOT RESET IN AN EFFECT. Holding
// `{path, status}` and comparing against the current path in render is what
// lets a recycled row start from 'loading' without a setState in the effect
// body (which triggers a cascading render, and which the react-hooks lint
// rule rejects). Because the virtualizer mounts a fresh component per row
// key, the common case is a new component rather than a recycled one — this
// only matters when the same key survives a path change, which it cannot,
// since the key IS the path. The reset is here for correctness, not speed.

import { useEffect, useState } from 'react';
import { getAlbumCoverUrl } from '@/api/client';
import { splitFullPath } from '@/lib/id3Reader';

interface Props {
  /** MUSIC_DIR-relative path including the filename. */
  fullPath: string;
  fileName: string;
  /** Used for the placeholder's initial letter; falls back to fileName. */
  fallbackTitle?: string;
  /** CSS gradient for the placeholder, so a row's placeholder matches
   *  everywhere the same song appears. */
  gradient: string;
}

type Status = 'loading' | 'ready' | 'absent';

interface LoadState {
  /** The path this state describes. A mismatch means "this row is showing
   *  someone else's result", which render resolves to 'loading'. */
  path: string;
  status: Status;
}

export function RowCover({ fullPath, fileName, fallbackTitle, gradient }: Props) {
  const [state, setState] = useState<LoadState>({
    path: fullPath,
    status: 'loading',
  });

  const { filePath, fileName: name } = splitFullPath(fullPath);
  const url = getAlbumCoverUrl(filePath, name);

  // The status for THIS path, discarding a stale entry from a previous one.
  const status: Status = state.path === fullPath ? state.status : 'loading';

  useEffect(() => {
    let cancelled = false;
    const img = new Image();
    img.onload = () => {
      if (!cancelled) setState({ path: fullPath, status: 'ready' });
    };
    img.onerror = () => {
      // 404 (no embedded cover) and any other failure land here. Both mean
      // "paint the placeholder": there is no retry loop, because a file
      // without art will not grow art by being asked again.
      if (!cancelled) setState({ path: fullPath, status: 'absent' });
    };
    img.src = url;

    return () => {
      cancelled = true;
      // Release the in-flight decode when the row scrolls out, so
      // fast-scrolling past 500 rows does not queue 500 image decodes.
      img.src = '';
    };
  }, [url, fullPath]);

  if (status === 'ready') {
    return (
      <img src={url} alt="" className="w-full h-full object-cover" />
    );
  }

  return (
    <div
      className="w-full h-full flex items-center justify-center text-white text-[10px] font-bold"
      style={{ background: gradient }}
      // The letter is the only thing here that changes shape, so keep it out
      // of the announcement: a screen reader should hear the row's title
      // once, from the text beside it.
      aria-hidden="true"
    >
      {(fallbackTitle || fileName).charAt(0)}
    </div>
  );
}
