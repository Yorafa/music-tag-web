/* Go Music Tag Web — GitHub Pages demo service worker.
 *
 * Why this file exists
 * --------------------
 * The published artifact is the REAL frontend build (vite output of
 * frontend/src, unmodified). That build is useless on a static host on its
 * own: `api/client.ts` hardcodes `baseURL: '/api/'` with no runtime
 * override, so every request 404s and the app sits on LoginPage forever
 * (`App.tsx` renders LoginPage until `loggedIn` is true).
 *
 * So we intercept. The workflow also rewrites the absolute `/api/` and
 * `/media/` literals in the bundle to sit under BASE (see demo/patch-bundle.mjs);
 * a service worker can only intercept requests inside its own scope, and the
 * default scope of a worker served at BASE/sw.js is exactly BASE/.
 *
 * Every response here is the gateway's uniform envelope
 * (`internal/gateway/handler/response.go`):
 *     { result: bool, code: "200"|"400", data: …, message: string }
 * Note `Failure()` answers HTTP 200 with result:false, so a mocked failure
 * is indistinguishable from a real one at the axios layer — which is
 * exactly the contract `api/envelope.ts` was written against. We mirror it.
 *
 * Nothing here is real data. It is a hand-built fixture library so the
 * deployed UI has something to render. Mutating endpoints are NOT mocked.
 */

const BASE = '/music-tag-web';

/* ------------------------------------------------------------------ *
 * envelope helpers
 * ------------------------------------------------------------------ */

const ok = (data, message = 'success') =>
  new Response(JSON.stringify({ result: true, code: '200', data, message }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });

/** Mirrors Failure(): HTTP 200 + result:false, so callers branch on
 *  data.result rather than on status — same as the real gateway. */
const fail = (message, code = '400') =>
  new Response(JSON.stringify({ result: false, code, data: [], message }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });

const notMocked = (ep) =>
  fail(`演示模式：${ep} 未提供模拟数据（本站不连接真实后端）`);

/* ------------------------------------------------------------------ *
 * fixture library
 * ------------------------------------------------------------------ */

// A 1x1 JPEG-ish placeholder cover, inlined so the page never reaches out
// to a third-party CDN. Real covers come back as data URIs from
// `/api/music_id3/` (see handler/tag.go), so this matches the real shape.
const COVER =
  'data:image/svg+xml;base64,' +
  btoaSafe(
    '<svg xmlns="http://www.w3.org/2000/svg" width="300" height="300">' +
      '<rect width="300" height="300" fill="#2a2f3a"/>' +
      '<circle cx="150" cy="150" r="54" fill="none" stroke="#8b93a7" stroke-width="6"/>' +
      '<circle cx="150" cy="150" r="10" fill="#8b93a7"/>' +
      '</svg>',
  );

function btoaSafe(s) {
  // btoa is byte-oriented; the fixture only has ASCII, but be explicit.
  let bin = '';
  for (let i = 0; i < s.length; i++) bin += String.fromCharCode(s.charCodeAt(i));
  return btoa(bin);
}

let nextId = 1;
const nid = () => nextId++;

const dir = (name, children, mtime) => ({
  id: nid(),
  name,
  title: name,
  icon: 'icon-folder',
  state: 'null',
  children,
  size: 4096,
  update_time: mtime,
});

/** `lrc` true → the track has a same-stem .lrc sidecar, which is exactly
 *  what makes the Go handler emit icon-script-files instead of
 *  icon-script-file (handler/file.go). */
const track = (name, size, mtime, lrc = false) => ({
  id: nid(),
  name,
  title: name,
  icon: lrc ? 'icon-script-files' : 'icon-script-file',
  state: 'null',
  children: [],
  size,
  update_time: mtime,
});

/** artist -> album -> tracks */
const LIBRARY = {
  'Aphex Twin': {
    'Selected Ambient Works 85-92': [
      ['Xtal (Intro).flac', 14523008, '2026-03-11 21:04:12', false],
      ['Tha (9:00).flac', 21341184, '2026-03-11 21:04:12', false],
      ['Pulsewidth.ape', 17825792, '2026-03-11 21:05:03', false],
      ['Ageispolis.ape', 19046400, '2026-03-11 21:05:03', false],
    ],
    'Selected Ambient Works Volume II': [
      ['Cliffs.flac', 33554432, '2026-03-11 21:09:41', false],
      ['Radiator.flac', 29821952, '2026-03-11 21:09:41', false],
    ],
  },
  'Miles Davis': {
    'Kind of Blue': [
      ['01 So What.flac', 56181248, '2026-02-02 19:32:07', false],
      ['02 Blue in Green.flac', 33832960, '2026-02-02 19:32:07', false],
      ['03 All Blues.flac', 58461760, '2026-02-02 19:32:09', false],
      ['04 Freddie Freeloader.flac', 58681344, '2026-02-02 19:32:09', false],
      ['05 Flamenco Sketches.flac', 54576640, '2026-02-02 19:32:11', true],
    ],
  },
  'Radiohead': {
    'OK Computer': [
      ['01 Airbag.flac', 29220864, '2026-01-18 08:15:22', true],
      ['02 Paranoid Android.flac', 47128576, '2026-01-18 08:15:22', false],
      ['03 Subterranean Homesick Alien.flac', 31457280, '2026-01-18 08:15:24', false],
      ['04 Exit Music (For a Film).flac', 27099136, '2026-01-18 08:15:24', false],
      ['05 Let Down.flac', 30359552, '2026-01-18 08:15:26', true],
    ],
    'Kid A': [
      ['01 Everything In Its Right Place.flac', 31457280, '2026-01-18 08:22:10', false],
      ['02 Idioteque.flac', 26214400, '2026-01-18 08:22:10', false],
    ],
  },
  'Brian Eno': {
    'Ambient 1: Music for Airports': [
      ['01 Landing.mp3', 4194304, '2025-12-05 23:11:00', false],
      ['02 1/1.mp3', 7340032, '2025-12-05 23:11:00', false],
      ['03 An Ending (Ascent).mp3', 8912896, '2025-12-05 23:12:31', true],
    ],
    'Discreet Music': [['01 Three Light Screens.mp3', 5242880, '2025-12-05 23:20:14', false]],
  },
  '坂本龍一': {
    async: [
      ['01 andata.flac', 34078720, '2026-04-09 14:07:55', false],
      ['02 triad.flac', 29147136, '2026-04-09 14:07:55', false],
      ['03 fullmoon.flac', 26214400, '2026-04-09 14:08:12', true],
    ],
  },
  窦唯: {
    黑梦: [
      ['01 黑梦.flac', 37748736, '2026-05-21 16:44:03', true],
      ['02 高级动物.flac', 46137344, '2026-05-21 16:44:03', true],
      ['03 悲情城市.flac', 52428800, '2026-05-21 16:45:19', false],
    ],
  },
};

const MTIME = '2026-09-20 11:22:33';

/** children for a library path. '' or '/' → artist dirs. */
function childrenFor(filePath) {
  const parts = filePath.split('/').filter(Boolean);
  if (parts.length === 0) {
    return Object.keys(LIBRARY).map((a) => dir(a, [], MTIME));
  }
  const [artist, album] = parts;
  const albums = LIBRARY[artist];
  if (!albums) return null;
  if (parts.length === 1) {
    return Object.keys(albums).map((al) => dir(al, [], MTIME));
  }
  const files = albums[album];
  if (!files) return null;
  return files.map(([n, s, t, l]) => track(n, s, t, l));
}

/** Mirrors handler/file.go: the response is a SINGLE root node wrapping
 *  the requested directory's children — not a bare list. */
function fileList(filePath) {
  const clean = (filePath || '').replace(/^\/+|\/+$/g, '');
  const children = childrenFor(clean);
  if (children === null) return fail('文件夹不存在');
  const name = clean === '' ? 'music' : clean.split('/').pop();
  return ok([
    {
      id: 0,
      name,
      title: name,
      icon: 'icon-folder',
      state: '',
      children,
      size: 0,
      update_time: '',
      expanded: true,
    },
  ]);
}

/* ------------------------------------------------------------------ *
 * music_id3 — MusicTagInfo (frontend/src/types/index.ts)
 * ------------------------------------------------------------------ */

const GENRES = {
  'Aphex Twin': ['电子', 'Ambient'],
  'Miles Davis': ['爵士', 'Jazz'],
  Radiohead: ['摇滚', 'Alternative'],
  'Brian Eno': ['电子', 'Ambient'],
  坂本龍一: ['电子', 'Ambient'],
  窦唯: ['摇滚', 'Experimental'],
};

function findTrack(filePath, fileName) {
  const parts = (filePath || '').split('/').filter(Boolean);
  const albums = LIBRARY[parts[0]];
  if (!albums) return null;
  const files = albums[parts[1]];
  if (!files) return null;
  const hit = files.find(([n]) => n === fileName);
  return hit ? { artist: parts[0], album: parts[1], entry: hit } : null;
}

function musicId3(filePath, fileName) {
  const found = findTrack(filePath, fileName);
  if (!found) return fail(`stat: no such file or directory: ${filePath}/${fileName}`);
  const { artist, album, entry } = found;
  const [name, size] = entry;
  const stem = name.replace(/\.[^.]+$/, '');
  const num = stem.match(/^(\d+)\s+(.*)$/);
  const title = num ? num[2] : stem;
  const genre = (GENRES[artist] || ['未知'])[0];
  const albumYear = {
    'Selected Ambient Works 85-92': '1992',
    'Selected Ambient Works Volume II': '1994',
    'Kind of Blue': '1959',
    'OK Computer': '1997',
    'Kid A': '2000',
    'Ambient 1: Music for Airports': '1978',
    DiscreetMusic: '1975',
    Discreet_Music: '1975',
    async: '2017',
    黑梦: '2004',
  }[album.replace(/\s/g, '_')] || album.replace(/\s/g, '_');
  return ok({
    title,
    filename: name,
    artist,
    album,
    albumartist: artist,
    genre,
    language: /[一-龥]/.test(title + artist) ? '中文' : '英文',
    year: String(1990 + (title.length % 30)),
    lyrics: entry[3]
      ? `[00:00.0]${title}\n[00:12.5]（演示数据：歌词为占位文本）\n[00:28.0]lyrics placeholder line 2\n[00:45.0]lyrics placeholder line 3\n`
      : '',
    comment: '',
    album_img: COVER,
    album_type: '专辑',
    discnumber: '',
    tracknumber: num ? num[1] : '',
    duration: String(120 + (title.length * 7)),
    bit_rate: String(900 + (title.length * 11)),
    size: String(size),
    artwork: COVER,
    artwork_w: '300',
    artwork_h: '300',
    artwork_size: String(size),
    is_save_lyrics_file: entry[3],
    is_save_album_cover: false,
  });
}

/* ------------------------------------------------------------------ *
 * search candidates — SongInfo
 * ------------------------------------------------------------------ */

const SOURCE_LABEL = {
  netease: '网易云音乐',
  kugou: '酷狗音乐',
  kuwo: '酷我音乐',
  migu: '咪咕音乐',
  qmusic: 'QQ 音乐',
  musicbrainz: 'MusicBrainz',
  acoustid: 'AcoustID 音频指纹',
};

function candidates(artist, title) {
  const year = String(1980 + (title.length * 3) % 45);
  const mk = (src, extra) => ({
    id: `${src}-${artist}-${title}`,
    name: title,
    artist,
    artist_id: `${src}-artist-${artist}`,
    album: extra.album,
    album_id: `${src}-album-${extra.album}`,
    album_img: COVER,
    year: extra.year || year,
    genre: extra.genre || '',
    source: src,
    ...(extra.acoustic ? { score: extra.acoustic } : {}),
    ...(extra.titleMatch ? { title_match: extra.titleMatch } : {}),
    ...(extra.lyric ? { lyrics: extra.lyric } : {}),
  });
  return [
    mk('netease', { album: `${title} (Deluxe)`, genre: '流行', titleMatch: 'exact' }),
    mk('kugou', { album: title, genre: '流行', titleMatch: 'exact' }),
    mk('kuwo', { album: title, year: String(Number(year) - 1), titleMatch: 'partial' }),
    mk('musicbrainz', {
      album: title,
      year,
      titleMatch: 'exact',
      genre: 'Rock',
    }),
    // AcoustID is the only source that listened to the audio, so it is
    // the only one allowed to carry a score (types/index.ts says so).
    mk('acoustid', { album: title, acoustic: 0.93, titleMatch: 'exact' }),
  ];
}

/* ------------------------------------------------------------------ *
 * sources — captured verbatim from a live gateway, so the sidebar /
 * scraper toggles render with the real names, kinds and flags.
 * ------------------------------------------------------------------ */

const SOURCES = [
  { name: 'acoustid', display_name: 'AcoustID 音频指纹', kind: 'tag', searchable: false, lyric: false, supports_id3: true, supports_audio_url: false, default_on: true },
  { name: 'kugou', display_name: '酷狗音乐', kind: 'tag', searchable: true, lyric: true, supports_id3: true, supports_audio_url: true, default_on: true },
  { name: 'kuwo', display_name: '酷我音乐', kind: 'tag', searchable: true, lyric: true, supports_id3: true, supports_audio_url: true, default_on: true },
  { name: 'migu', display_name: '咪咕音乐', kind: 'tag', searchable: true, lyric: true, supports_id3: true, supports_audio_url: true, default_on: true },
  { name: 'musicbrainz', display_name: 'MusicBrainz', kind: 'tag', searchable: true, lyric: false, supports_id3: true, supports_audio_url: false, default_on: true },
  { name: 'netease', display_name: '网易云音乐', kind: 'tag', searchable: true, lyric: true, supports_id3: true, supports_audio_url: true, default_on: true },
  { name: 'qmusic', display_name: 'QQ 音乐', kind: 'tag', searchable: true, lyric: true, supports_id3: true, supports_audio_url: true, default_on: true },
  { name: 'youtube', display_name: 'YouTube', kind: 'download', searchable: true, lyric: true, supports_id3: false, supports_audio_url: true, default_on: false },
];

const OPERATION_LOGS = [
  { id: 3, action: 'update_id3', target: 'Miles Davis/Kind of Blue/05 Flamenco Sketches.flac', operator: 'admin', status: 'success', item_count: 1, details: '{"album":"Kind of Blue","artist":"Miles Davis","title":"Flamenco Sketches"}', created_at: '2026-09-20T11:20:02+08:00' },
  { id: 2, action: 'scrape', target: 'Radiohead/OK Computer/01 Airbag.flac', operator: 'admin', status: 'success', item_count: 5, details: '{"source":"netease","candidates":5,"applied":1}', created_at: '2026-09-20T11:12:44+08:00' },
  { id: 1, action: 'check_duplicate', target: 'Aphex Twin/Selected Ambient Works 85-92', operator: 'admin', status: 'success', item_count: 4, details: '{"rows":4,"duplicates":0,"suspects":1}', created_at: '2026-09-19T22:03:10+08:00' },
];

/* ------------------------------------------------------------------ *
 * tiny silent WAV, so the player bar gets a decodable stream instead of
 * a 404 (44-byte canonical header + one silent sample frame).
 * ------------------------------------------------------------------ */

function silentWav() {
  const dataLen = 8;
  const buf = new ArrayBuffer(44 + dataLen);
  const v = new DataView(buf);
  const ascii = (off, s) => { for (let i = 0; i < s.length; i++) v.setUint8(off + i, s.charCodeAt(i)); };
  ascii(0, 'RIFF');
  v.setUint32(4, 36 + dataLen, true);
  ascii(8, 'WAVE');
  ascii(12, 'fmt ');
  v.setUint32(16, 16, true);          // PCM chunk size
  v.setUint16(20, 1, true);           // format = PCM
  v.setUint16(22, 1, true);           // channels
  v.setUint32(24, 8000, true);        // sample rate
  v.setUint32(28, 16000, true);       // byte rate
  v.setUint16(32, 2, true);           // block align
  v.setUint16(34, 16, true);          // bits per sample
  ascii(36, 'data');
  v.setUint32(40, dataLen, true);
  return buf;
}

const WAV = silentWav();

/* ------------------------------------------------------------------ *
 * routing
 * ------------------------------------------------------------------ */

async function readJson(request) {
  try {
    return await request.json();
  } catch {
    return {};
  }
}

function query(url) {
  return Object.fromEntries(url.searchParams.entries());
}

async function handle(request, url) {
  // /music-tag-web/api/<endpoint>
  const ep = url.pathname.slice((BASE + '/api/').length);
  const body = request.method === 'POST' ? await readJson(request) : {};

  switch (ep) {
    case 'token/': {
      // LoginPage does its own fetch() and reads data.access, then hands it
      // to useAuthStore.login() — which persists it and flips loggedIn.
      // So the login screen, the form validation and the transition into
      // HomePage are all real; only the credential check is fake.
      return ok({ access: 'demo-token', refresh: '', token_type: 'jwt' });
    }

    case 'sources/':
      return ok(SOURCES);

    case 'active_queue/':
      return ok({
        pending: [],
        queues: ['default'],
        servers: [
          {
            ID: 'demo-server',
            Host: 'pages',
            PID: 1,
            Concurrency: 10,
            Queues: { critical: 10, default: 8, low: 6 },
            StrictPriority: false,
            Started: '2026-09-20T03:00:00Z',
            Status: 'active',
            ActiveWorkers: [],
          },
        ],
      });

    case 'operation_logs/': {
      const q = query(url);
      const size = Number(q.page_size || 20) || 20;
      const page = Number(q.page || 1) || 1;
      const start = (page - 1) * size;
      return ok({
        count: OPERATION_LOGS.length,
        page,
        page_size: size,
        results: OPERATION_LOGS.slice(start, start + size),
      });
    }

    case 'file_list/':
      return fileList(body.file_path);

    case 'music_id3/':
      return musicId3(body.file_path, body.file_name);

    case 'search_music/':
    case 'fetch_id3_by_title/': {
      const artist = body.artist || body.artist_name || '';
      const title = body.title || body.name || '';
      return ok(candidates(String(artist), String(title)));
    }

    case 'fetch_lyric/':
      return ok(
        `[00:00.0]${body.title || 'unknown'} — ${body.artist || 'unknown'}\n` +
          '[00:12.5]（演示数据：歌词为占位文本）\n' +
          '[00:28.0]lyrics placeholder line 2\n',
      );

    case 'check_duplicate/': {
      // RowDuplicate shape — only content-level evidence counts as a
      // duplicate (see README: 文件名相同不算重复).
      const rows = (body.paths || body.rows || []).map((p, i) => ({
        id: typeof p === 'string' ? p : p.id,
        state: i === 1 ? 'duplicate' : i === 2 ? 'suspect' : 'unique',
        reason: i === 1 ? 'SHA-256 一致' : i === 2 ? '声纹匹配 0.91' : '',
      }));
      return ok(rows);
    }

    case 'tag/preview_parse_filenames/':
    case 'tag/preview_rename_from_tags/': {
      // Both previews answer { results: ParsedPreviewRow[] } AFTER the
      // client unwraps the envelope (api/envelope.ts). status is per-row.
      const dirPath = body.file_path || body.dir || '';
      const children = childrenFor(String(dirPath).split('/').filter(Boolean).slice(0, -1).join('/')) || [];
      return ok({
        results: children
          .filter((c) => c.icon !== 'icon-folder')
          .map((c, i) => ({
            file_name: c.name,
            artist: i % 2 === 0 ? c.name.replace(/\s*-\s*.*/, '') : '',
            title: i % 2 === 0 ? c.name.replace(/^.*?\s*-\s*/, '') : c.name,
            status: i === 0 ? 'ok' : 'missing',
          })),
      });
    }

    /* Mutating endpoints are deliberately not mocked. A demo that
     * pretends to write files is worse than one that says it can't. */
    case 'update_id3/':
    case 'batch_update_id3/':
    case 'tag/apply_parsed_filenames/':
    case 'tag/apply_rename_from_tags/':
    case 'delete_files/':
    case 'tidy_folder/':
    case 'prune_empty_folders/':
    case 'operation_logs/clear/':
    case 'clear_async_tasks/':
    case 'full_scan_folder/':
    case 'upload_image/':
    case 'download/':
      return notMocked(ep);

    default:
      return notMocked(ep);
  }
}

self.addEventListener('install', (event) => {
  event.waitUntil(self.skipWaiting());
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET' && req.method !== 'POST') return;

  let url;
  try {
    url = new URL(req.url);
  } catch {
    return;
  }
  if (url.origin !== self.location.origin) return;

  const apiPrefix = `${BASE}/api/`;
  const mediaPrefix = `${BASE}/media/`;

  if (url.pathname.startsWith(apiPrefix)) {
    event.respondWith(handle(req, url).catch((err) => fail(`演示模式内部错误: ${err.message}`)));
    return;
  }

  if (url.pathname.startsWith(mediaPrefix)) {
    // /media/<rel>/<name> is the byte stream for <audio> and browser-side
    // id3 reads. Serve silence so the player does not error out loudly.
    event.respondWith(
      new Response(WAV, {
        status: 200,
        headers: {
          'Content-Type': 'audio/wav',
          'Content-Length': String(WAV.byteLength),
          'Accept-Ranges': 'bytes',
        },
      }),
    );
  }
});
