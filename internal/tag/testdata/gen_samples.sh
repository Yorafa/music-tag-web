#!/bin/sh
# Generate one short real audio sample per container/codec the tag layer
# claims to support. Used by TestFormatMatrix (read) and
# TestWriteReadRoundTrip (write): both need *real* files, because the whole
# point of those tests is to catch the case where a parser accepts a file
# and then silently drops every field.
#
# Samples are generated rather than committed: a 1s silent file is ~2-30 KB
# per format but there are 15 of them across two codecs each, and binary
# blobs in git make format regressions impossible to eyeball in a diff.
#
# Requires ffmpeg on PATH. The Go tests skip (not fail) without it, so a
# machine without ffmpeg still gets a green suite, just a smaller one.
set -eu

out="${1:?usage: gen_samples.sh <outdir>}"
mkdir -p "$out"

# 1 second, 44.1kHz, stereo, near-silence with a tiny tone so the encoder
# does not emit a zero-length stream some containers reject.
tone="sine=frequency=440:duration=1:sample_rate=44100"

# format  args
# --------------------------  ---------------------------------------------
# MPEG Layer III            libmp3lame
# AAC in MP4                aac
# ALAC in MP4                alac
# FLAC                      flac
# Ogg Vorbis                libvorbis
# Ogg Opus                  libopus
# Ogg Speex                 libspeex
# WAV PCM                   pcm_s16le
# AIFF                      pcm_s16be (aiff muxer)
# WMA (ASF)                 wmav2
# WavPack                   wavpack
# Musepack SV8              libmpcdec / mpc
# APE (Monkey's Audio)       ape
# True Audio                 tta
# DSD (dsf) / DSDIFF (dff)  needs -f dsf / dff, pcm_s24le source

gen() {
	name="$1"; shift
	# shellcheck disable=SC2068
	ffmpeg -hide_banner -loglevel error -y -f lavfi -i "$tone" $@ "$out/$name" \
		|| echo "skip: $name ($*)" >&2
}

gen sample.mp3        -c:a libmp3lame -b:a 128k
gen sample.m4a        -c:a aac -b:a 128k
gen sample_alac.m4a   -c:a alac
gen sample.flac       -c:a flac
gen sample.ogg        -c:a libvorbis -b:a 128k
gen sample.opus       -c:a libopus -b:a 96k
gen sample.spx        -c:a libspeex
gen sample.wav        -c:a pcm_s16le
gen sample.aiff       -c:a pcm_s16be -f aiff
gen sample.wma        -c:a wmav2 -b:a 128k
gen sample.wv         -c:a wavpack
gen sample.tta        -c:a tta

# ape / mpc / dsf / dff need muxers that a stock distro ffmpeg usually lacks
# (they are decoder-side in ffmpeg). The tests treat a missing sample as
# "this build cannot exercise that format" and skip it, so attempt them
# quietly rather than letting gen() print a scary skip line.

gen sample.mpc        -c:a libmpcdec 2>/dev/null || true
gen sample.ape        -c:a ape       2>/dev/null || true
gen sample.dsf        -c:a pcm_s24le -ar 352800 -f dsf 2>/dev/null || true
gen sample.dff        -c:a pcm_s24le -ar 352800 -f dff 2>/dev/null || true

# ffmpeg happily leaves a 0-byte file behind when the muxer is missing;
# drop those so the tests do not try to parse an empty file.
find "$out" -type f -size 0 -delete

ls -1 "$out"
