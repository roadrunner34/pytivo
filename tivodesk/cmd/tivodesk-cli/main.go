// Command tivodesk-cli runs pieces of TivoDesk from a terminal, for testing
// against real TiVos and for timing.
//
//	tivodesk-cli discover [-wait 5s]
//	tivodesk-cli npl [-wait 5s] [-tivo NAME]
//	tivodesk-cli download -tivo NAME [-match TEXT] [-ps] [-dir FOLDER]
//	tivodesk-cli faketivo [-n 684] [-mak 1234567890] [-mb 1] [-delay 0]
//
// npl and download read the Media Access Key from the TIVO_MAK environment
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
	"path/filepath"
	"strings"
	"time"

	"tivodesk/internal/config"
	"tivodesk/internal/discovery"
	"tivodesk/internal/tivo"
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
	case "faketivo":
		fake(os.Args[2:])
	default:
		usage()
	}
}
