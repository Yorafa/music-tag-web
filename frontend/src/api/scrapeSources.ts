// Asking several tag sources for candidates at once.
//
// Both scrape entry points (the batch toolbar and the track detail dialog)
// need the same thing: the sources the user ticked, searched in parallel,
// merged into one ranked list. Neither could do it before — the toolbar
// searched `selectedSources[0]` and threw the rest away, and the detail
// dialog searched a hardcoded 'smart_tag'.

import { fetchId3ByTitle } from '@/api/client';
import { mergeCandidates, withSource } from '@/components/detail/candidates';
import { CANDIDATE_FETCH_LIMIT } from '@/components/common/tagSources';
import type { SongInfo } from '@/types';
import type { MusicSource } from '@/types';

export interface MultiSourceResult {
  candidates: SongInfo[];
  /** Sources that answered with nothing, and sources that failed. Kept
   *  apart from "found nothing" because they are different news: one is a
   *  verdict, the other is an outage the user may want to retry. */
  emptySources: string[];
  failedSources: string[];
  /** The first failure message, for the notice bar. */
  error?: string;
}

/** Search every source in parallel and merge the answers.
 *
 *  Parallel, not sequential: the sources are independent network calls and
 *  a six-source search that takes the sum of six latencies is the reason a
 *  user stops waiting for results. `allSettled` rather than `all` for the
 *  same reason — one source being down must not cost the user the other
 *  five.
 *
 *  `limit` is passed per source, not to the merge: the gateway truncates
 *  each source's own answer, and truncating the merged list afterwards
 *  would throw away the tail of a source whose best rows were ranked
 *  below another's. */
export async function searchAcrossSources(
  query: string,
  sources: MusicSource[],
  fullPath = '',
  limit: number = CANDIDATE_FETCH_LIMIT,
): Promise<MultiSourceResult> {
  const results = await Promise.allSettled(
    sources.map((source) =>
      fetchId3ByTitle(query, source, fullPath, limit).then((res) => res?.data ?? []),
    ),
  );

  const perSource: SongInfo[][] = [];
  const emptySources: string[] = [];
  const failedSources: string[] = [];
  let error: string | undefined;

  results.forEach((r, i) => {
    const source = sources[i];
    if (r.status === 'rejected') {
      failedSources.push(source);
      error ??= r.reason instanceof Error ? r.reason.message : String(r.reason);
      perSource.push([]);
      return;
    }
    if (!Array.isArray(r.value) || r.value.length === 0) {
      emptySources.push(source);
      perSource.push([]);
      return;
    }
    perSource.push(withSource(r.value, source));
  });

  return {
    candidates: mergeCandidates(perSource),
    emptySources,
    failedSources,
    error,
  };
}
