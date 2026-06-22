import asyncio
import binascii
import json
import time
import requests
from Cryptodome.Cipher import AES

from applications.task.services.acoust import AcoustidClient
from applications.task.services.kugou import KugouClient
from applications.task.services.kuwo import KuwoClient
from applications.task.services.smart_tag_resource import SmartTagClient
from applications.task.utils import timestamp_to_dt


class MusicResource:
    def __init__(self, info):
        self.resource = self.get_resource(info)

    def get_resource(self, info):
        if info == "netease":
            return NetEaseMusicClient()
        elif info == "migu":
            return MiGuMusicClient()
        elif info == "qmusic":
            return QmusicClient()
        elif info == "kugou":
            return KugouClient()
        elif info == "kuwo":
            return KuwoClient()
        elif info == "acoustid":
            return AcoustidClient()
        elif info == "musicbrainz":
            return MusicBrainzClient()
        elif info == "smart_tag":
            return SmartTagClient()
        raise Exception("暂不支持该音乐平台")

    def fetch_lyric(self, song_id):
        try:
            return self.resource.fetch_lyric(song_id)
        except Exception as e:
            print("音乐平台歌词获取失败", e)
            return ""

    def fetch_id3_by_title(self, title):
        try:
            return self.resource.fetch_id3_by_title(title)
        except Exception as e:
            print("音乐平台搜索失败", e)
            return []


# 网易云音乐使用公开 GET API，无需加密


class NetEaseMusicClient:
    """网易云音乐 - 搜索用 linux/forward (AES加密) 获取专辑封面，歌词用公开 GET API"""
    BASE_URL = "https://music.163.com"
    HEADERS = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
        "Referer": "https://music.163.com/",
    }
    # AES-ECB 密钥 (参考 music-dl)
    _AES_KEY = binascii.unhexlify("7246674226682325323F5E6544673A51")

    @staticmethod
    def _encode_netease_data(data):
        """AES-ECB 加密 eparams (linux/forward 接口)"""
        raw = json.dumps(data)
        pad = 16 - len(raw) % 16
        raw += chr(pad) * pad
        cipher = AES.new(NetEaseMusicClient._AES_KEY, AES.MODE_ECB)
        return binascii.hexlify(cipher.encrypt(raw.encode("utf-8"))).upper().decode()

    def fetch_lyric(self, song_id):
        try:
            resp = requests.get(
                f"{self.BASE_URL}/api/song/lyric",
                params={"id": song_id, "lv": -1, "kv": -1, "tv": -1},
                headers=self.HEADERS,
                timeout=10,
            )
            return resp.json().get("lrc", {}).get("lyric", "")
        except Exception as e:
            print("网易云歌词获取失败", e)
            return ""

    def fetch_id3_by_title(self, title):
        try:
            eparams = {
                "method": "POST",
                "url": "http://music.163.com/api/cloudsearch/pc",
                "params": {"s": title, "type": 1, "offset": 0, "limit": 10},
            }
            data = {"eparams": self._encode_netease_data(eparams)}
            resp = requests.post(
                f"{self.BASE_URL}/api/linux/forward",
                data=data,
                headers=self.HEADERS,
                timeout=10,
            )
            songs = resp.json().get("result", {}).get("songs", [])
        except Exception as e:
            print("网易云音乐搜索失败", e)
            return []
        for song in songs:
            # cloudsearch/pc 返回 weapi 字段名: ar (artists), al (album)
            artists = song.get("ar", [])
            album = song.get("al", {})
            if artists:
                artist = ",".join([a.get("name", "") for a in artists])
                artist_id = artists[0].get("id", "")
            else:
                artist = ""
                artist_id = ""
            # publishTime 可能在顶层或 album 内
            year = song.get("publishTime") or album.get("publishTime", "")
            if year:
                year = timestamp_to_dt(year / 1000, "%Y")
            # al.picUrl 来自 weapi，是真正的专辑封面直链
            cover = album.get("picUrl") or ""
            song["artist"] = artist
            song["artist_id"] = artist_id
            song["album"] = album.get("name", "")
            song["album_id"] = album.get("id", "")
            song["album_img"] = cover
            song["year"] = year or ""
        return songs


class MusicBrainzClient:
    """MusicBrainz 开放音乐元数据库 — 免费、无需认证、1 req/s 限速"""
    BASE_URL = "https://musicbrainz.org/ws/2"
    HEADERS = {"User-Agent": "MusicTagWeb/2.0 ( personal-use; https://github.com )"}

    def fetch_lyric(self, song_id):
        raise Exception("MusicBrainz 不提供歌词")

    def fetch_id3_by_title(self, title):
        time.sleep(1.1)  # MusicBrainz 要求每秒最多 1 个请求
        params = {
            "query": f'recording:"{title}"',
            "fmt": "json",
            "limit": 10,
        }
        try:
            resp = requests.get(
                f"{self.BASE_URL}/recording/",
                params=params,
                headers=self.HEADERS,
                timeout=10,
            )
            data = resp.json()
        except Exception as e:
            print("MusicBrainz 搜索失败", e)
            return []

        songs = []
        for rec in data.get("recordings", []):
            artist_credit = rec.get("artist-credit", [])
            artist = "".join(ac.get("name", "") + (ac.get("joinphrase", "") or "") for ac in artist_credit).strip()
            artist_id = artist_credit[0].get("artist", {}).get("id", "") if artist_credit else ""

            releases = rec.get("releases", [])
            album = releases[0].get("title", "") if releases else ""

            tags = rec.get("tags", [])
            genre = tags[0].get("name", "") if tags else ""

            songs.append({
                "id": rec.get("id", ""),
                "name": rec.get("title", ""),
                "artist": artist,
                "artist_id": artist_id,
                "album": album,
                "album_id": "",
                "album_img": "",
                "year": "",
                "genre": genre,
            })
        return songs


class MiGuMusicClient:
    """咪咕音乐 - 使用 pd.musicapp.migu.cn 公开 API (参考 music-dl)"""
    BASE_URL = "http://pd.musicapp.migu.cn/MIGUM2.0/v1.0/content"
    HEADERS = {
        "User-Agent": "Mozilla/5.0 (iPhone; CPU iPhone OS 13_2_3 like Mac OS X) AppleWebKit/605.1.15",
        "Referer": "http://music.migu.cn/",
    }

    def fetch_lyric(self, song_id):
        """song_id 实际为搜索返回的 lyricUrl，直接 GET 获取 LRC 文本"""
        if not song_id:
            return ""
        try:
            resp = requests.get(song_id, headers=self.HEADERS, timeout=10)
            resp.encoding = "utf-8"
            return resp.text
        except Exception as e:
            print("咪咕歌词获取失败", e)
            return ""

    def fetch_id3_by_title(self, title):
        try:
            params = {
                "ua": "Android_migu",
                "version": "5.0.1",
                "text": title,
                "pageNo": 1,
                "pageSize": 10,
                "searchSwitch": '{"song":1,"album":0,"singer":0,"tagSong":0,"mvSong":0,"songlist":0,"bestShow":1}',
            }
            resp = requests.get(
                f"{self.BASE_URL}/search_all.do",
                params=params,
                headers=self.HEADERS,
                timeout=10,
            )
            songs = resp.json().get("songResultData", {}).get("result", [])
        except Exception as e:
            print("咪咕音乐搜索失败", e)
            return []
        results = []
        for item in songs:
            singers = [s.get("name", "") for s in item.get("singers", [])]
            albums = item.get("albums", [])
            imgs = item.get("imgItems", [])
            lyric_url = item.get("lyricUrl", item.get("trcUrl", ""))
            results.append({
                "id": lyric_url or "",
                "name": item.get("name", ""),
                "artist": "/".join(singers),
                "artist_id": "",
                "album": albums[0].get("name", "") if albums else "",
                "album_id": "",
                "album_img": imgs[0].get("img", "") if imgs else "",
                "year": "",
            })
        return results


class QmusicClient:
    """QQ音乐 - 使用 qqmusic-api-python 库，自带签名算法"""

    def __init__(self):
        try:
            from qqmusic_api import Client
            self._client = Client()
        except ImportError:
            raise Exception("请安装 qqmusic-api-python: uv pip install qqmusic-api-python")

    @staticmethod
    def _await(req):
        """桥接 qqmusic-api 的 awaitable 对象到同步调用"""
        async def _wrap():
            return await req
        return asyncio.run(_wrap())

    def fetch_lyric(self, song_id):
        try:
            lyric_resp = self._await(self._client.lyric.get_lyric(str(song_id)))
            lyric_resp = lyric_resp.decrypt()
            return lyric_resp.lyric or ""
        except Exception as e:
            print("QQ音乐歌词获取失败", e)
            return ""

    def fetch_id3_by_title(self, title):
        try:
            resp = self._await(
                self._client.search.search_by_type(title, search_type=0, num=10)
            )
            songs = []
            for s in resp.song:
                artists = ",".join(singer.name for singer in s.singer)
                songs.append({
                    "id": s.mid,
                    "mid": s.mid,
                    "name": s.name or s.title,
                    "artist": artists,
                    "artist_id": str(s.singer[0].id) if s.singer else "",
                    "album": s.album.name,
                    "album_id": s.album.mid,
                    "album_img": s.cover_url(300),
                    "year": s.time_public[:4] if s.time_public else "",
                })
            return songs
        except Exception as e:
            print("QQ音乐搜索失败", e)
            return []
