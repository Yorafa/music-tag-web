// How many files a preview shows, in one place.
//
// Three dialogs answer the same "what would this do to my library?" —
// 整理目录, 解析文件名, 从标签改名 — and all three had the same problem:
// they previewed and rendered the WHOLE selection. On a 2000-row
// selection that is a table nobody reads, a request that does real work
// per row (a stat, a tag read, a filename parse), and a UI whose answer
// arrives too late to be useful.
//
// Ten is enough to see what a rule does. The point of a preview is to
// recognise the SHAPE of the result, not to audit every row of it; the
// apply is what touches everything, and it reports its own per-file
// outcomes in 操作审计.
//
// # The cap is not the same thing in all three, and the difference matters
//
//   - 整理目录 and 从标签改名: the preview REQUEST is capped. It is
//     read-only, and the apply carries its own path list, so previewing
//     10 and applying 2000 is exactly right — and the request does 1/200th
//     of the filesystem work it used to.
//
//   - 解析文件名: the preview's response IS the apply's authority. The
//     server mints a token over the paths it previewed, and Apply writes
//     whatever that token holds. Capping the request would therefore cap
//     the WRITE — a user who selected 300 files and clicked 解析文件名
//     would get 10 written and a success toast. So there the request stays
//     whole and only the TABLE is capped. The trade is deliberate: an
//     unsaved 10 files is a much smaller disaster than a silent 290-file
//     shortfall nobody was told about.

export const PREVIEW_LIMIT = 10;

/** Cap a list to the preview limit. Applied to a request's paths, or to
 *  the rows a table renders — which one is safe depends on the endpoint,
 *  and the three dialogs each say so at their call site. */
export function capPreview<T>(items: T[]): T[] {
  return items.slice(0, PREVIEW_LIMIT);
}

/** The sentence under a truncated preview table, for a dialog whose apply
 *  is independent of the preview.
 *
 *  It has to carry both numbers. "只预览了 10 个" alone reads as a bug,
 *  and "预览了 10 个" alone hides that the rest were never looked at. */
export function previewTruncationNote(previewed: number, total: number): string | null {
  if (total <= PREVIEW_LIMIT) return null;
  return `只预览了前 ${previewed} 个，共 ${total} 个 —— 预览只用来确认规则长什么样，真正执行仍会处理全部 ${total} 个。`;
}

/** The variant for a dialog whose apply is bound to the preview's token,
 *  where the operator is about to write files. */
export function previewTruncationNoteBound(
  previewed: number,
  total: number,
): string | null {
  if (total <= PREVIEW_LIMIT) return null;
  return `下面只列出前 ${previewed} 个，共 ${total} 个；提交时仍会写入全部 ${total} 个。`;
}
