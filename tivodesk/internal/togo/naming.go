package togo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// badChars replaces the characters Windows does not allow in file names,
// with the same substitutions pyTivo makes.
var badChars = strings.NewReplacer(
	`\`, "-", "/", "-", ":", " -", ";", ",", "*", ".",
	"?", ".", "!", ".", `"`, "'", "<", "(", ">", ")", "|", " ",
)

// maxNameLength keeps names well inside Windows' 255-character limit, with
// room for " (12).tivo.part.txt".
const maxNameLength = 200

// FileName returns the name to save a recording under, in the style of TiVo
// Desktop:
//
//	Title - 'Episode' (Recorded Mar 05, 2021, KQED) (TS).tivo
//
// or, when sortable is set, a form that sorts by date:
//
//	Title - 2021-03-05 - 'Episode' (KQED) (TS).tivo
//
// A decrypted recording is an ordinary video file, so it drops the (TS) or
// (PS) mark and ends in .ts or .mpg instead.
//
// The date is shown in loc. count is 1 for the first file of that name; a
// higher count adds " (count)" to tell duplicates apart.
func FileName(r Recording, ts, decrypted, sortable bool, loc *time.Location, count int) string {
	name := r.Title
	if name == "" {
		name = "Recording " + r.ID
	}
	when := r.Captured.In(loc)
	episode := ""
	if r.EpisodeTitle != "" {
		episode = " - '" + r.EpisodeTitle + "'"
	}

	var tail string
	if sortable {
		tail = " - " + when.Format("2006-01-02") + episode
		if r.Station != "" {
			tail += " (" + r.Station + ")"
		}
	} else {
		tail = episode + " (Recorded " + when.Format("Jan 02, 2006")
		if r.Station != "" {
			tail += ", " + r.Station
		}
		tail += ")"
	}
	ext := ".tivo"
	switch {
	case decrypted && ts:
		ext = ".ts"
	case decrypted:
		ext = ".mpg"
	case ts:
		tail += " (TS)"
	default:
		tail += " (PS)"
	}
	if count > 1 {
		tail += fmt.Sprintf(" (%d)", count)
	}

	name, tail = badChars.Replace(name), badChars.Replace(tail)
	// A very long title is cut, never the part that makes the name unique.
	if room := maxNameLength - len([]rune(tail)); len([]rune(name)) > room {
		name = string([]rune(name)[:max(room, 20)])
	}
	return strings.TrimLeft(name+tail, " ") + ext
}

// exists reports whether a file is present.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// PartSuffix marks a download that has not finished.
const PartSuffix = ".part"

// FreePath returns a path in dir for a recording that no finished or
// unfinished download already uses.
func FreePath(dir string, r Recording, ts, decrypted, sortable bool, loc *time.Location) string {
	for count := 1; ; count++ {
		path := filepath.Join(dir, FileName(r, ts, decrypted, sortable, loc, count))
		if !exists(path) && !exists(path+PartSuffix) {
			return path
		}
	}
}
