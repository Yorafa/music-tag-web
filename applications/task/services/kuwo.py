import requests
from urllib.parse import urlencode

try:
    import demjson3
except ImportError:
    demjson3 = None


class KuwoClient:
    """酷我音乐 - 使用 search.kuwo.cn/r.s 遗留接口 (参考 go-music-dl)"""
    HEADERS = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36",
    }

    @staticmethod
    def _parse_legacy_json(text):
        """解析酷我遗留接口的 JS 风格 JSON（单引号、无引号 key）"""
        if demjson3 is None:
            raise Exception("请安装 demjson3: uv pip install demjson3")
        return demjson3.decode(text)

    def fetch_lyric(self, song_id):
        """返回 {"lyric": str, "cover": str} — 同一 API 调用同时获取歌词和封面"""
        try:
            resp = requests.get(
                "http://m.kuwo.cn/newh5/singles/songinfoandlrc",
                params={"musicId": song_id, "httpsStatus": "1"},
                headers=self.HEADERS,
                timeout=10,
            )
            data = resp.json()
            lrclist = data.get("data", {}).get("lrclist", [])
            cover = data.get("data", {}).get("songinfo", {}).get("pic", "")
        except Exception as e:
            print("酷我歌词获取失败", e)
            return {"lyric": "", "cover": ""}
        lyric = ""
        try:
            for line in lrclist:
                seconds = int(float(line.get("time", "0")))
                m, s = divmod(seconds, 60)
                h, m = divmod(m, 60)
                time_format = "%d:%02d:%02d" % (h, m, s)
                content = line.get("lineLyric", "")
                lyric += f"[{time_format}]{content}\n"
        except Exception as e:
            print("酷我歌词解析异常", e)
        return {"lyric": lyric, "cover": cover}

    def fetch_id3_by_title(self, title):
        try:
            params = {
                "all": title,
                "ft": "music",
                "itemset": "web_2013",
                "client": "kt",
                "pcmp4": "1",
                "geo": "c",
                "vipver": "1",
                "pn": "0",
                "rn": "10",
                "rformat": "json",
                "encoding": "utf8",
            }
            resp = requests.get(
                "http://search.kuwo.cn/r.s?" + urlencode(params),
                headers=self.HEADERS,
                timeout=10,
            )
            data = self._parse_legacy_json(resp.text)
            songs = data.get("abslist", data.get("musiclist", []))
        except Exception as e:
            print("酷我音乐搜索失败", e)
            return []
        results = []
        for s in songs:
            if not isinstance(s, dict):
                continue
            name = s.get("NAME", s.get("SONGNAME", ""))
            artist = s.get("ARTIST", s.get("SINGER", ""))
            album = s.get("ALBUM", "")
            rid = str(s.get("MUSICRID", s.get("musicrid", ""))).replace("MUSIC_", "")
            results.append({
                "id": rid,
                "name": name,
                "artist": artist,
                "artist_id": str(s.get("ARTISTID", "")),
                "album": album,
                "album_id": str(s.get("ALBUMID", "")),
                "album_img": "",
                "year": "",
            })
        return results


