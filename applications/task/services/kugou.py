import hashlib
import time

import requests


def getSignature(text):
    """酷狗签名 — 纯 Python MD5，无 execjs 依赖"""
    return hashlib.md5(text.encode()).hexdigest().upper()


class KugouClient:

    def fetch_lyric(self, song_id):
        url = f'http://m.kugou.com/app/i/krc.php?cmd=100&timelength=999999&hash={song_id}'
        html = requests.get(url)
        html.encoding = 'utf-8'
        txt = html.text
        return txt

    def fetch_id3_by_title(self, title):
        millis = str(round(time.time() * 1000))
        KEY_CODE = "NVPh5oo715z5DIWAeQlhMDsWXXQV4hwtbitrate=0clienttime={time}clientver=2000dfid=-inputtype=0iscorrection=1isfuzzy=0keyword={keyword}mid={time}page=1pagesize=10platform=WebFilterprivilege_filter=0srcappid=2919tag=emuserid=-1uuid={time}NVPh5oo715z5DIWAeQlhMDsWXXQV4hwt"
        p = KEY_CODE.format(time=millis, keyword=title)
        signature = getSignature(p)
        URL_SEARCH = "https://complexsearch.kugou.com/v2/search/song?keyword={keyword}&page=1&pagesize=10&bitrate=0&isfuzzy=0&tag=em&inputtype=0&platform=WebFilter&userid=-1&clientver=2000&iscorrection=1&privilege_filter=0&srcappid=2919&clienttime={time}&mid={time}&uuid={time}&dfid=-&signature={signature}"
        url = URL_SEARCH.format(keyword=title, time=millis, signature=signature)
        response = requests.get(url=url)
        json_dict = response.json()
        songs = json_dict.get("data", {}).get("lists")
        for song in songs:
            artists = song['SingerName'].replace("<em>", "").replace("</em>", "")
            song["artist"] = ",".join(artists.split("、"))
            song["id"] = song['FileHash']
            song["name"] = song['SongName'].replace("<em>", "").replace("</em>", "")
            song["artist_id"] = song['SingerId']
            song["album"] = song['AlbumName']
            song["album_id"] = song['AlbumID']
            song["album_img"] = song['Image'].format(size=150)
            song["year"] = song['PublishTime']
        return songs


