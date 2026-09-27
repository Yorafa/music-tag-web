// What happened to each row of a batch scrape, and how to say it.
//
// Split out of handleRunBatchScrape because that function is already the
// longest in the toolbar, and because the summary it used to print —
// "成功 N 首，未匹配 M 首" — is the thing this replaces: it cannot say
// WHICH source answered for a row, so a track that failed looks identical
// to a track whose source was simply not in the ticked set.

/** How one row ended up. Four outcomes, not two: a row can fail to match
 *  (a fact about the music), be refused by the writer (a fact about the
 *  file), or error out (a fact about the network). Collapsing them into
 *  "failed" is what made the batch button untrustworthy. */
export type ScrapeRowStatus = 'written' | 'no_match' | 'refused' | 'error';

export interface ScrapeRowOutcome {
  fullPath: string;
  fileName: string;
  status: ScrapeRowStatus;
  /** The source that produced the candidate that was written. Null when
   *  nothing was written, or when the source is unknown. */
  source: string | null;
  /** What was actually written, for the rows that got tags. */
  applied?: { title?: string; artist?: string; album?: string };
  /** Why not. A count is not an explanation: "3 个未匹配" gives the user
   *  nothing to act on, whereas "所选音源都没有该曲" does. */
  reason?: string;
  /** Sources that were asked and returned nothing / errored, so the user
   *  can tell "nobody has this track" from "one of my sources is down". */
  emptySources?: string[];
  failedSources?: string[];
}

export interface ScrapeReportSummary {
  total: number;
  written: number;
  noMatch: number;
  refused: number;
  errored: number;
  /** Sources that produced at least one written row, most used first. */
  sourcesUsed: string[];
}

/** Count the outcomes and rank the sources that actually contributed.
 *
 *  Ranked by rows won, not by the order they were ticked: "哪个源命中"
 *  is a question about this run, and a source that answered for twenty
 *  rows is more useful to name first than one that answered for one. */
export function summarizeScrape(rows: ScrapeRowOutcome[]): ScrapeReportSummary {
  const wins = new Map<string, number>();
  let written = 0;
  let noMatch = 0;
  let refused = 0;
  let errored = 0;
  for (const row of rows) {
    switch (row.status) {
      case 'written':
        written += 1;
        if (row.source) wins.set(row.source, (wins.get(row.source) ?? 0) + 1);
        break;
      case 'no_match':
        noMatch += 1;
        break;
      case 'refused':
        refused += 1;
        break;
      case 'error':
        errored += 1;
        break;
    }
  }
  return {
    total: rows.length,
    written,
    noMatch,
    refused,
    errored,
    sourcesUsed: [...wins.entries()]
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
      .map(([id]) => id),
  };
}

/** A source id as the user knows it. Plugins are addressed by id
 *  (`netease`), and a report full of ids is a report nobody reads; an id
 *  this build does not know is passed through rather than hidden, because
 *  "some source I don't recognise" is information too. */
export function sourceLabel(id: string, names: { id: string; name: string }[]): string {
  return names.find((s) => s.id === id)?.name ?? id;
}

/** The reason to show when a row produced nothing, preferring the most
 *  specific one available. A row whose sources all errored and a row whose
 *  sources all came back empty need different words, and the one the
 *  report shows is the one the user can act on. */
export function noMatchReason(
  emptySources: string[],
  failedSources: string[],
  autoApplyOff: boolean,
): string {
  if (failedSources.length > 0) {
    return `${failedSources.length} 个音源请求失败：${failedSources.join('、')}`;
  }
  if (emptySources.length > 0) {
    return `${emptySources.length} 个音源均无结果：${emptySources.join('、')}`;
  }
  if (autoApplyOff) {
    return '已关闭「自动采纳高置信度结果」，未写入任何标签';
  }
  return '所选音源没有返回候选';
}
