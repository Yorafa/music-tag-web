/**
 * 路径层工具。所有方法围绕「相对 MUSIC_DIR 的相对路径」语义——
 * `''` 代表 MUSIC_DIR 根目录（容器内即 /app/media），`'foo/bar'` 是其下的 foo/bar。
 *
 * 三条契约的来源（详见 router.go + handler/file.go 的注释）：
 *  1. /api/file_list/、/api/music_id3/、/api/update_id3/、/api/fetch_id3_by_title/
 *     全部走 `utils.SafeJoin(MUSIC_DIR, filePath)`，所以相对路径直接作为参数；
 *  2. /api/media_url/?path=<rel>、fileBrowser.tsx 的 buildMediaUrl 把相对路径
 *     拼成 /media/<rel>/<name>，与容器内 /app/media/<rel>/<name> 对应；
 *  3. 前端持久化 / 用户可读串加上 `/music/` 前缀作为稳定别名（与 docker
 *     compose ./music → /app/media 的惯例一致）。
 */

export const PATH_ALIAS = '/music';

/** 相对路径 → 用户可读显示串。'' → '/music'，'foo/bar' → '/music/foo/bar'。 */
export function formatDisplayPath(rel: string): string {
  const trimmed = rel.replace(/\/+$/, '').replace(/^\/+/, '');
  return trimmed ? `${PATH_ALIAS}/${trimmed}` : PATH_ALIAS;
}

/**
 * 用户可读输入 → 相对路径。
 *   '' | '/' | '/music' | 'music' | 无意义输入 → ''
 *   '/music/foo' | 'music/foo' | 'foo' | 'foo/bar/' → 'foo' | 'foo/bar'
 * 迁移兼容：旧版本若把宿主绝对路径写到 localStorage（'/mnt/.../music/...'）
 * 且含 /music/ 子串，截取其后；不含则**直接丢弃回到根**——避免把
 * '/mnt/ssd0/code/music-tag-web/music/歌' 当相对字面值透传到后端 SafeJoin
 * 必然 404 的尴尬。
 */
export function parseDisplayPath(input: string): string {
  const trimmed = input.trim().replace(/\/+$/, '').replace(/^\/+/, '');
  if (trimmed === '' || trimmed === 'music' || trimmed === PATH_ALIAS.slice(1)) return '';
  const idx = trimmed.indexOf('/music/');
  if (idx >= 0) return trimmed.slice(idx + '/music/'.length);
  if (trimmed.startsWith('music/')) return trimmed.slice('music/'.length);
  // 既不是空 / 'music' / '/music'，也不以 '/music/' 或 'music/' 起头——
  // 大概率是迁移期遗留的宿主绝对路径（如 /mnt/ssd0/code/music-tag-web/foo）。
  // 直接回根比把它当相对字面值透传给后端 SafeJoin 更安全：后者会让前端
  // 看到"目录存在但全是空"的假象，而 SafeJoin 兜底的拒绝路径更不友好。
  return '';
}

/** 把面包屑 parts 数组还原成相对路径；空数组 → ''。 */
export function joinParts(parts: string[]): string {
  return parts.filter(Boolean).join('/');
}

/**
 * 决定 哪个 path 传给 /api/file_list/。空串是「相对 MUSIC_DIR 根」
 * 的有效值——`||` 会把它 coerce 成 stale filePath，导致 back-to-root 不刷
 * 新。`??` 在 caller *完全没传*时才回退。
 */
export function resolveBrowsePath(
  passed: string | undefined,
  fallback: string,
): string {
  return passed ?? fallback;
}
