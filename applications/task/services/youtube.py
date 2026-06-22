import os
import subprocess
from django.conf import settings


def search_youtube(query, max_results=10):
    """
    Search YouTube using yt-dlp's search functionality.
    Returns a list of dicts with video info.
    """
    try:
        import yt_dlp
    except ImportError:
        return [{"error": "yt-dlp is not installed. Please install it first."}]

    ydl_opts = {
        "quiet": True,
        "no_warnings": True,
        "extract_flat": True,
        "skip_download": True,
    }

    search_query = f"ytsearch{max_results}:{query}"
    results = []

    try:
        with yt_dlp.YoutubeDL(ydl_opts) as ydl:
            info = ydl.extract_info(search_query, download=False)
            entries = info.get("entries", [])
            for entry in entries:
                results.append({
                    "id": entry.get("id", ""),
                    "title": entry.get("title", ""),
                    "duration": entry.get("duration", 0),
                    "url": f"https://www.youtube.com/watch?v={entry.get('id', '')}",
                    "channel": entry.get("channel", ""),
                    "thumbnail": entry.get("thumbnails", [{}])[-1].get("url", "") if entry.get("thumbnails") else "",
                })
    except Exception as e:
        return [{"error": str(e)}]

    return results


def download_song(video_id, download_dir=None):
    """
    Download audio from a YouTube video using yt-dlp.
    Converts to OGG format.

    Args:
        video_id: YouTube video ID
        download_dir: Directory to save the file. Defaults to MEDIA_ROOT/music/downloads

    Returns:
        dict with download result info
    """
    if download_dir is None:
        download_dir = os.path.join(settings.MEDIA_ROOT, "music", "downloads")

    os.makedirs(download_dir, exist_ok=True)

    url = f"https://www.youtube.com/watch?v={video_id}"

    # Use yt-dlp CLI via subprocess for reliable format remuxing
    cmd = [
        "yt-dlp",
        "-x",  # extract audio only
        "--remux-video", "webm>ogg/opus>ogg/aac>m4a",
        "-o", os.path.join(download_dir, "%(title)s.%(ext)s"),
        "--no-playlist",
        "--print", "after_move:filepath",
        url,
    ]

    try:
        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=600,  # 10 minute timeout for long videos
        )

        if result.returncode != 0:
            error_msg = result.stderr.strip() or "Unknown error"
            return {
                "success": False,
                "error": error_msg,
                "video_id": video_id,
            }

        # The last line of stdout should be the downloaded file path
        output_lines = [l for l in result.stdout.strip().split("\n") if l]
        file_path = output_lines[-1] if output_lines else ""

        if file_path and os.path.exists(file_path):
            file_name = os.path.basename(file_path)
            return {
                "success": True,
                "video_id": video_id,
                "file_path": file_path,
                "file_name": file_name,
                "relative_path": os.path.relpath(file_path, settings.MEDIA_ROOT),
            }
        else:
            # Try to find the file in the download directory
            stdout = result.stdout
            return {
                "success": True,
                "video_id": video_id,
                "file_path": file_path,
                "file_name": os.path.basename(file_path) if file_path else "",
                "stdout": stdout,
                "note": "File saved but path could not be determined precisely.",
            }

    except subprocess.TimeoutExpired:
        return {
            "success": False,
            "error": "Download timed out (10 minutes)",
            "video_id": video_id,
        }
    except FileNotFoundError:
        return {
            "success": False,
            "error": "yt-dlp executable not found. Please install yt-dlp.",
            "video_id": video_id,
        }
    except Exception as e:
        return {
            "success": False,
            "error": str(e),
            "video_id": video_id,
        }
