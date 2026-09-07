package main

import (
	"regexp"
	"strings"
)

// These are filename hints, not verified media properties.
type releaseInfo struct{ resolution, source, video, audio, language, display, note string }

var releaseTokens = regexp.MustCompile(`[^A-Z0-9]+`)

func describeRelease(name string) releaseInfo {
	tokens := " " + releaseTokens.ReplaceAllString(strings.ToUpper(plain(name)), " ") + " "
	has := func(words ...string) bool {
		for _, word := range words {
			if strings.Contains(tokens, " "+word+" ") {
				return true
			}
		}
		return false
	}
	first := func(options ...string) string {
		for _, v := range options {
			if has(v) {
				return v
			}
		}
		return ""
	}
	r := releaseInfo{resolution: first("2160P", "1080P", "1080I", "720P", "576P", "480P"), note: "Title tags are unverified; source details may clarify this release."}
	if r.resolution == "" && has("4K", "UHD") {
		r.resolution = "2160P"
	}
	switch {
	case has("HDCAM", "CAM", "HDTS", "TS", "TELESYNC", "TC", "TELECINE"):
		r.source = "CAM/TS"
		r.note = "Camera/early copy: picture and sound may be poor."
	case has("REMUX"):
		r.source = "REMUX"
		r.note = "Remux: usually a larger file preserving the source streams."
	case has("BLURAY", "BLU RAY", "BDRIP", "BRRIP"):
		r.source = "Blu-ray"
		r.note = "Disc source; an encode trades some quality for a smaller file."
	case has("WEB DL", "WEBDL"):
		r.source = "WEB-DL"
		r.note = "Web source: often a useful balance of quality and file size."
	case has("WEBRIP"):
		r.source = "WEBRip"
		r.note = "Web capture/re-encode; quality depends on the encoding."
	case has("HDTV", "DVDRIP", "DVD"):
		r.source = first("HDTV", "DVDRIP", "DVD")
	}
	switch {
	case has("AV1"):
		r.video = "AV1"
	case has("H265", "H 265", "X265", "HEVC"):
		r.video = "H.265/HEVC"
	case has("H264", "H 264", "X264", "AVC"):
		r.video = "H.264/AVC"
	}
	if has("ATMOS") {
		r.audio = "Atmos"
	} else {
		r.audio = first("TRUEHD", "DTS HD", "DTS", "DDP", "DDP5", "EAC3", "AC3", "AAC", "FLAC", "MP3")
	}
	if r.audio == "DDP5" {
		r.audio = "DDP"
	}
	if has("7 1") {
		r.audio += " 7.1"
	} else if has("5 1") || has("DDP5 1", "AAC5 1") {
		r.audio += " 5.1"
	}
	r.language = first("MULTI", "DUAL", "ENG", "ENGLISH", "FRENCH", "GERMAN", "SPANISH", "HINDI", "JAPANESE", "KOREAN", "ITALIAN", "RUSSIAN")
	if has("DV", "DOVI", "DOLBY VISION") {
		r.display = "Dolby Vision"
	} else if has("HDR", "HDR10", "HDR10PLUS") {
		r.display = "HDR"
	}
	if r.source != "CAM/TS" {
		if r.display != "" {
			r.note = "HDR/Dolby Vision: check display and player support for correct colors."
		} else if r.video == "H.265/HEVC" || r.video == "AV1" {
			r.note = r.video + ": efficient compression; older devices may need a compatible player."
		}
	}
	return r
}

func joinDetails(values ...string) string {
	var parts []string
	for _, s := range values {
		if s = plain(s); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}

func (r releaseInfo) summary() string {
	tags := joinDetails(r.resolution, r.source, r.video, r.audio, r.display, r.language)
	if tags == "" {
		return "No recognizable media tags · check the source page"
	}
	return tags
}
