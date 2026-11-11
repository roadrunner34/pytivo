// Command tivodesk-cli runs pieces of TivoDesk from a terminal, for testing
// against real TiVos and for timing.
//
//	tivodesk-cli discover [-wait 5s]
//	tivodesk-cli npl [-wait 5s] [-tivo NAME]
//	tivodesk-cli download -tivo NAME [-match TEXT] [-ps] [-dir FOLDER]
//	tivodesk-cli decrypt [-check] [-out FOLDER] FILE.tivo ...
//	tivodesk-cli faketivo [-n 684] [-mak 1234567890] [-mb 1] [-delay 0]
//
// npl, download and decrypt read the Media Access Key from the TIVO_MAK environment
// variable, so that it stays out of shell history. The key is never
// printed.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tivodesk/internal/config"
	"tivodesk/internal/discovery"
	"tivodesk/internal/tivo"
	"tivodesk/internal/tivocrypt/decrypt"
	"tivodesk/internal/togo"
	"tivodesk/internal/togo/faketivo"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  tivodesk-cli discover [-wait 5s]            list the TiVos on the network
  tivodesk-cli npl [-wait 5s] [-tivo NAME]    time a Now Playing fetch (needs TIVO_MAK)
  tivodesk-cli download -tivo NAME [-match TEXT] [-ps] [-dir FOLDER]
                                              download one recording, the smallest
                                              whose title contains TEXT (needs TIVO_MAK)
  tivodesk-cli decrypt [-check] [-out FOLDER] FILE.tivo ...
                                              decrypt recordings (needs TIVO_MAK); -check
                                              has ffmpeg read each result and count errors
  tivodesk-cli faketivo [-n 684] [-mak KEY]   run a pretend TiVo for trying the app`)
	os.Exit(2)
}

// find browses the network for the given time and returns what it found.
func find(wait time.Duration) []tivo.Box {
	var reg tivo.Registry
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	discovery.Browse(ctx, &reg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return reg.List()
}

func discover(args []string) {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	wait := fs.Duration("wait", 5*time.Second, "how long to listen")
	fs.Parse(args)

	start := time.Now()
	boxes := find(*wait)
	if len(boxes) == 0 {
		fmt.Printf("No TiVos answered within %s.\n", *wait)
		os.Exit(1)
	}
	fmt.Printf("%-20s %-10s %-16s %-6s %-18s %s\n", "NAME", "MODEL", "ADDRESS", "PORT", "TSN", "SOFTWARE")
	for _, b := range boxes {
		fmt.Printf("%-20s %-10s %-16s %-6d %-18s %s\n", b.Name, b.Model(), b.Address, b.Port, b.TSN, b.SWVersion)
	}
	fmt.Printf("\n%d found in %s.\n", len(boxes), time.Since(start).Round(time.Millisecond))
}

func npl(args []string) {
	fs := flag.NewFlagSet("npl", flag.ExitOnError)
	wait := fs.Duration("wait", 5*time.Second, "how long to look for TiVos")
	only := fs.String("tivo", "", "only this TiVo, by name (default: all)")
	fs.Parse(args)

	mak := os.Getenv("TIVO_MAK")
	if mak == "" {
		fmt.Fprintln(os.Stderr, "Set the TIVO_MAK environment variable to your Media Access Key first.")
		os.Exit(2)
	}

	failed := false
	for _, b := range find(*wait) {
		if *only != "" && !strings.EqualFold(*only, b.Name) {
			continue
		}
		client := togo.NewClient(b.Address, b.Port, mak)

		start := time.Now()
		list, err := client.NowPlaying(context.Background(), nil)
		cold := time.Since(start)
		if err != nil {
			fmt.Printf("%-20s FAILED: %v\n", b.Name, err)
			failed = true
			continue
		}

		// What a refresh costs when nothing has changed.
		start = time.Now()
		marker, _, err := client.Summary(context.Background())
		warm := time.Since(start)
		unchanged := err == nil && marker == list.LastChange

		series := map[string]bool{}
		var bytes int64
		for _, r := range list.Recordings {
			series[r.Title] = true
			bytes += r.Size
		}
		fmt.Printf("%-20s %4d recordings, %3d titles, %6.1f GB   full fetch %s   unchanged check %s (unchanged=%v)\n",
			b.Name, len(list.Recordings), len(series), float64(bytes)/1e9,
			cold.Round(time.Millisecond), warm.Round(time.Millisecond), unchanged)
	}
	if failed {
		os.Exit(1)
	}
}

// download fetches one recording from a real TiVo, still encrypted, with
// its details file. It is for checking downloads against real hardware and
// for collecting files to test decryption with.
func download(args []string) {
	fs := flag.NewFlagSet("download", flag.ExitOnError)
	wait := fs.Duration("wait", 5*time.Second, "how long to look for TiVos")
	name := fs.String("tivo", "", "the TiVo to download from, by name")
	match := fs.String("match", "", "only recordings whose title or episode contains this")
	ps := fs.Bool("ps", false, "ask for a program stream instead of a transport stream")
	dir := fs.String("dir", ".", "the folder to save in")
	fs.Parse(args)

	mak := os.Getenv("TIVO_MAK")
	if mak == "" {
		fmt.Fprintln(os.Stderr, "Set the TIVO_MAK environment variable to your Media Access Key first.")
		os.Exit(2)
	}
	var box *tivo.Box
	for _, b := range find(*wait) {
		if strings.EqualFold(*name, b.Name) {
			box = &b
			break
		}
	}
	if box == nil {
		fmt.Fprintf(os.Stderr, "No TiVo called %q answered. Run \"tivodesk-cli discover\" to see their names.\n", *name)
		os.Exit(1)
	}

	ctx := context.Background()
	client := togo.NewClient(box.Address, box.Port, mak)
	list, err := client.NowPlaying(ctx, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not read Now Playing:", err)
		os.Exit(1)
	}
	var pick *togo.Recording
	needle := strings.ToLower(*match)
	for i, r := range list.Recordings {
		if r.CopyProtected || r.InProgress || r.Size == 0 ||
			!strings.Contains(strings.ToLower(r.Title+" "+r.EpisodeTitle), needle) {
			continue
		}
		if pick == nil || r.Size < pick.Size {
			pick = &list.Recordings[i]
		}
	}
	if pick == nil {
		fmt.Fprintln(os.Stderr, "No downloadable recording matches.")
		os.Exit(1)
	}

	ts := !*ps && config.IsTSCapable(box.TSN)
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	path := togo.FreePath(*dir, *pick, ts, false, time.Local)
	fmt.Printf("%s: %s %s\n  about %.0f MB, as a %s\n  to %s\n", box.Name, pick.Title, pick.EpisodeTitle,
		float64(pick.Size)/1e6, map[bool]string{true: "transport stream", false: "program stream"}[ts], path)

	f, err := os.Create(path + togo.PartSuffix)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	start, last := time.Now(), time.Now()
	res, err := client.Download(ctx, pick.ContentURL, f, togo.DownloadOptions{
		TS: ts,
		Progress: func(written int64, losses int) {
			if time.Since(last) >= time.Second {
				last = time.Now()
				fmt.Printf("\r  %6.0f MB  %5.1f MB/s  %d damaged packets ", float64(written)/1e6,
					float64(written)/1e6/time.Since(start).Seconds(), losses)
			}
		},
	})
	f.Close()
	fmt.Println()
	if err != nil {
		os.Remove(path + togo.PartSuffix)
		fmt.Fprintln(os.Stderr, "FAILED:", err)
		os.Exit(1)
	}
	if err := os.Rename(path+togo.PartSuffix, path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	took := time.Since(start)
	fmt.Printf("Done: %d bytes in %s (%.1f MB/s), %d damaged packets.\n", res.Bytes, took.Round(time.Second),
		float64(res.Bytes)/1e6/took.Seconds(), res.SyncLosses)

	meta, err := client.Details(ctx, *pick)
	if err != nil {
		fmt.Println("The full details could not be read, so the basics were saved:", err)
	}
	if txt, err := os.Create(path + ".txt"); err == nil {
		meta.Write(txt)
		txt.Close()
		fmt.Printf("Details saved to %s (%d lines).\n", filepath.Base(path)+".txt", len(meta))
	}
}

// findFFmpeg returns the ffmpeg to use: the one named, the one in pyTivo's
// bin folder if this is run from the repository, or the one on the PATH.
func findFFmpeg(named string) string {
	if named != "" {
		return named
	}
	for _, candidate := range []string{`..\bin\ffmpeg.exe`, `bin\ffmpeg.exe`} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "ffmpeg"
}

// checkWithFFmpeg has ffmpeg decode a whole file and reports what it
// found. A stream that was decrypted wrongly is noise to a decoder, so it
// produces errors by the thousand; a good one produces few or none.
func checkWithFFmpeg(ffmpeg, path string) bool {
	// What ffmpeg thinks is in the file. This exits with an error, because
	// no output is asked for; only what it prints matters.
	info, _ := exec.Command(ffmpeg, "-hide_banner", "-i", path).CombinedOutput()
	streams := 0
	for _, line := range strings.Split(string(info), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "Stream #") || strings.HasPrefix(line, "Duration:") {
			fmt.Println("    " + line)
			if strings.HasPrefix(line, "Stream #") {
				streams++
			}
		}
	}

	start := time.Now()
	out, err := exec.Command(ffmpeg, "-hide_banner", "-v", "error", "-i", path, "-f", "null", "-").CombinedOutput()
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	fmt.Printf("    ffmpeg decoded it in %s: %d streams, %d error lines", time.Since(start).Round(time.Second), streams, len(lines))
	if err != nil {
		fmt.Printf(", and it stopped with: %v", err)
	}
	fmt.Println()
	for i, line := range lines {
		if i == 8 {
			fmt.Printf("      ... and %d more\n", len(lines)-i)
			break
		}
		fmt.Println("      " + line)
	}
	return err == nil && streams > 0
}

// decryptFiles decrypts recordings already on disk. It is the check that
// decryption works on real files.
func decryptFiles(args []string) {
	fs := flag.NewFlagSet("decrypt", flag.ExitOnError)
	out := fs.String("out", "", "the folder to write to (default: beside each recording)")
	check := fs.Bool("check", false, "have ffmpeg decode each result and count its errors")
	ffmpeg := fs.String("ffmpeg", "", "the ffmpeg to use for -check")
	fs.Parse(args)

	mak := os.Getenv("TIVO_MAK")
	if mak == "" {
		fmt.Fprintln(os.Stderr, "Set the TIVO_MAK environment variable to your Media Access Key first.")
		os.Exit(2)
	}
	if fs.NArg() == 0 {
		usage()
	}
	// PowerShell does not expand wildcards for programs, so do it here.
	var files []string
	for _, arg := range fs.Args() {
		if matches, _ := filepath.Glob(arg); len(matches) > 0 {
			files = append(files, matches...)
		} else {
			files = append(files, arg)
		}
	}

	failed := false
	for _, path := range files {
		fmt.Println(filepath.Base(path))
		in, err := os.Open(path)
		if err != nil {
			fmt.Println("  FAILED:", err)
			failed = true
			continue
		}
		dir := filepath.Dir(path)
		if *out != "" {
			dir = *out
			os.MkdirAll(dir, 0o755)
		}
		// The extension depends on the kind of stream, which is not known
		// until the header has been read; write to a temporary name first.
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		tmp := filepath.Join(dir, base+".decrypting")
		f, err := os.Create(tmp)
		if err != nil {
			in.Close()
			fmt.Println("  FAILED:", err)
			failed = true
			continue
		}
		start := time.Now()
		stats, err := decrypt.Decrypt(in, f, mak)
		size, _ := f.Seek(0, io.SeekCurrent)
		in.Close()
		f.Close()
		if err != nil {
			os.Remove(tmp)
			fmt.Println("  FAILED:", err)
			failed = true
			continue
		}
		dest := filepath.Join(dir, base+stats.Extension())
		os.Remove(dest)
		if err := os.Rename(tmp, dest); err != nil {
			fmt.Println("  FAILED:", err)
			failed = true
			continue
		}
		took := time.Since(start)
		fmt.Printf("  -> %s\n  %.0f MB in %s (%.0f MB/s); %d packets, %d of them encrypted",
			dest, float64(size)/1e6, took.Round(time.Millisecond), float64(size)/1e6/max(took.Seconds(), 0.001),
			stats.Packets, stats.Decrypted)
		if stats.Undecryptable > 0 || stats.SkippedBytes > 0 {
			fmt.Printf("; %d dropped for want of a key, %d damaged bytes skipped", stats.Undecryptable, stats.SkippedBytes)
		}
		fmt.Println()
		if *check && !checkWithFFmpeg(findFFmpeg(*ffmpeg), dest) {
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

// fake runs a pretend TiVo until interrupted. Point the app at it with
// the TIVODESK_EXTRA_TIVO environment variable.
func fake(args []string) {
	fs := flag.NewFlagSet("faketivo", flag.ExitOnError)
	n := fs.Int("n", 684, "number of recordings")
	mak := fs.String("mak", "1234567890", "the Media Access Key it accepts")
	mb := fs.Int("mb", 1, "the size of each recording it sends, in megabytes")
	delay := fs.Duration("delay", 0, "a pause between each 64 KB it sends, to imitate a slow TiVo")
	fs.Parse(args)

	s := faketivo.Start(*mak, *n)
	s.SetPackets(*mb * 1_000_000 / 188)
	s.SlowDownloads(*delay)
	defer s.Close()
	host, port := s.Addr()
	fmt.Printf("Pretend TiVo with %d recordings at %s:%d, accepting the key %s.\n", *n, host, port, *mak)
	fmt.Printf("Start the app with TIVODESK_EXTRA_TIVO=Pretend=%s:%d to see it.\n", host, port)
	select {} // until the process is killed
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "discover":
		discover(os.Args[2:])
	case "npl":
		npl(os.Args[2:])
	case "download":
		download(os.Args[2:])
	case "decrypt":
		decryptFiles(os.Args[2:])
	case "faketivo":
		fake(os.Args[2:])
	default:
		usage()
	}
}
