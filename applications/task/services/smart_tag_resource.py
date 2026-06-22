from applications.task.utils import match_score, match_artist
from concurrent.futures import ThreadPoolExecutor

from component import music_tag


class SmartTagClient:

    def fetch_lyric(self, song_id):
        pass

    def run(self, resource, title):
        from applications.task.services.music_resource import MusicResource
        songs = MusicResource(resource).fetch_id3_by_title(title)
        for song in songs:
            song["resource"] = resource
        return songs

    def fetch_id3_by_title(self, info):

        title = info["title"]
        full_path = info["full_path"]
        file = music_tag.load_file(full_path)
        artist = file["artist"].value or ""
        album = file["album"].value or ""
        order_songs = []
        seen_ids = set()
        max_score = 0

        # 搜索源（按优先级排列，kuwo 已失效暂移除）
        sources = ["qmusic", "netease", "kugou", "migu", "musicbrainz"]
        with ThreadPoolExecutor(max_workers=5) as pool:
            results = pool.map(self.run, sources, [title] * len(sources))

        for songs in results:
            for song in songs:
                sid = str(song.get("id", ""))
                if sid and sid in seen_ids:
                    continue
                if sid:
                    seen_ids.add(sid)

                title_score = match_score(title, song["name"])
                artist_score = match_artist(artist if artist else title, song["artist"])
                album_score = match_score(album if album else title, song["album"])
                if artist and artist_score == 0:
                    artist_score = -2
                if not artist and artist_score >= 1:
                    if title_score >= 1:
                        title_score = 2
                song["score"] = title_score + artist_score + album_score
                max_score = max(max_score, song["score"])
                if title_score == 0:
                    continue
                order_songs.append(song)
        order_songs.sort(key=lambda x: x["score"], reverse=True)
        # 去重：同标题同艺术家只保留最高分
        deduped = []
        seen_titles = set()
        for s in order_songs:
            dedup_key = (s.get("name", "").lower(), s.get("artist", "").lower())
            if dedup_key not in seen_titles:
                seen_titles.add(dedup_key)
                deduped.append(s)
        return deduped[:15]
