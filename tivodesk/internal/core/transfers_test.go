package core

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tivodesk/internal/events"
	"tivodesk/internal/secrets"
	"tivodesk/internal/store"
	"tivodesk/internal/togo"
	"tivodesk/internal/togo/faketivo"
)

// A TSN that can send transport streams, as every TiVo since the Premiere
// can.
const tsTSN = "8460001"

// downloader returns an engine with a fake TiVo found, a key stored, a
// recordings folder chosen and the TiVo's list loaded.
func downloader(t *testing.T, dir string, fake *faketivo.Server) (*Engine, string) {
	t.Helper()
	store := secrets.NewMemory()
	store.Set(secrets.MAK, testMAK)
	e := newEngine(t, dir, store)

	out := filepath.Join(dir, "recordings")
	cfg := e.Config()
	cfg.Server["togo_path"] = out
	cfg.Server["togo_decrypt"] = "false" // these tests are about the raw download
	if _, err := e.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	addFake(e, fake, tsTSN)
	if err := e.RefreshNowPlaying(tsTSN); err != nil {
		t.Fatal(err)
	}
	waitLoaded(t, e, tsTSN)
	return e, out
}

// waitTransfers blocks until every transfer has finished one way or
// another, and returns them.
func waitTransfers(t *testing.T, e *Engine) []store.Job {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		jobs := e.Transfers()
		busy := false
		for _, j := range jobs {
			busy = busy || !j.IsFinished()
		}
		if !busy {
			return jobs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("transfers never finished: %+v", e.Transfers())
	return nil
}

func TestDownloadQueue(t *testing.T) {
	fake := faketivo.New(t, testMAK, 30)
	e, out := downloader(t, t.TempDir(), fake)

	var changes, progress int
	e.Events().Subscribe(func(ev events.Event) {
		switch ev.Topic {
		case events.TransfersChanged:
			changes++
		case events.TransferProgress:
			progress++
		}
	})

	// 1003 and 1004 are ordinary; 1009 is copy protected, 1000 is still
	// recording, and 1 does not exist.
	n, err := e.QueueDownloads(tsTSN, []string{"1003", "1004", "1009", "1000", "1"}, "")
	if err != nil || n != 2 {
		t.Fatalf("queued %d, %v; want 2", n, err)
	}
	jobs := waitTransfers(t, e)
	if len(jobs) != 2 {
		t.Fatalf("%d transfers, want 2", len(jobs))
	}
	want := faketivo.Recording(4000, true)
	for _, j := range jobs {
		if j.State != store.Done || j.Error != "" || j.Bytes != int64(len(want)) || !j.TS || j.Attempts != 1 ||
			j.Started.IsZero() || j.Finished.IsZero() {
			t.Errorf("transfer: %+v", j)
		}
		got, err := os.ReadFile(j.Path)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: %v; contents match: %v", j.Path, err, bytes.Equal(got, want))
		}
		if filepath.Dir(j.Path) != out || !strings.HasSuffix(j.Path, "(TS).tivo") {
			t.Errorf("saved as %s", j.Path)
		}
		txt, err := os.ReadFile(j.Path + ".txt")
		if err != nil || !strings.Contains(string(txt), "vActor: Lovelace|Ada") {
			t.Errorf("details file: %v\n%s", err, txt)
		}
		if _, err := os.Stat(j.Path + togo.PartSuffix); err == nil {
			t.Error("the partial file was left behind")
		}
	}
	if !strings.Contains(filepath.Base(jobs[0].Path), "Show 3 & Friends - 'Episode 3'") {
		t.Errorf("first file is named %s", filepath.Base(jobs[0].Path))
	}
	// A TiVo sends one recording at a time, so the fake answers "busy" to
	// a second request while one is under way. Both arriving proves the
	// queue ran them in turn.
	if got := fake.Downloads(); len(got) != 2 {
		t.Errorf("the TiVo saw %d downloads, want 2", len(got))
	}
	if changes < 4 {
		t.Errorf("only %d change events for two downloads", changes)
	}

	// Downloading the same recording again gets a new name.
	if n, err := e.QueueDownloads(tsTSN, []string{"1003"}, "ps"); err != nil || n != 1 {
		t.Fatalf("second copy: %d, %v", n, err)
	}
	jobs = waitTransfers(t, e)
	if last := jobs[2]; last.State != store.Done || last.TS || !strings.HasSuffix(last.Path, "(PS).tivo") {
		t.Errorf("program-stream copy: %+v", last)
	}

	if n := e.ClearTransfers(); n != 3 || len(e.Transfers()) != 0 {
		t.Errorf("cleared %d, %d left", n, len(e.Transfers()))
	}
	if _, err := os.Stat(jobs[0].Path); err != nil {
		t.Error("clearing the list must not delete the files")
	}
}

func TestQueueRefusals(t *testing.T) {
	fake := faketivo.New(t, testMAK, 30)
	dir := t.TempDir()
	mem := secrets.NewMemory()
	e := newEngine(t, dir, mem)

	if _, err := e.QueueDownloads(tsTSN, []string{"1003"}, ""); !errors.Is(err, ErrUnknownTiVo) {
		t.Errorf("unknown TiVo: %v", err)
	}
	addFake(e, fake, tsTSN)
	if _, err := e.QueueDownloads(tsTSN, []string{"1003"}, ""); !errors.Is(err, ErrNoMAK) {
		t.Errorf("no key: %v", err)
	}
	e.SetMAK(testMAK)
	if _, err := e.QueueDownloads(tsTSN, []string{"1003"}, ""); !errors.Is(err, ErrNoFolder) {
		t.Errorf("no folder: %v", err)
	}

	// The folder may be given as the name of a share.
	cfg := e.Config()
	cfg.Server["togo_path"] = "Videos"
	cfg.Share["Videos"] = map[string]string{"type": "video", "path": filepath.Join(dir, "videos")}
	e.SaveConfig(cfg)
	e.RefreshNowPlaying(tsTSN)
	waitLoaded(t, e, tsTSN)

	for id, want := range map[string]string{"1009": "copy-protected", "1000": "still being recorded", "5": "no longer on the TiVo"} {
		if _, err := e.QueueDownloads(tsTSN, []string{id}, ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("recording %s: got %v, want something about %q", id, err, want)
		}
	}
	if _, err := e.QueueDownloads(tsTSN, []string{"1003"}, "avi"); err == nil {
		t.Error("an unknown format should be refused")
	}

	if n, err := e.QueueDownloads(tsTSN, []string{"1003"}, ""); err != nil || n != 1 {
		t.Fatalf("queue: %d, %v", n, err)
	}
	jobs := waitTransfers(t, e)
	if filepath.Dir(jobs[0].Path) != filepath.Join(dir, "videos") {
		t.Errorf("saved in %s, not the share's folder", filepath.Dir(jobs[0].Path))
	}
}

func TestCancelAndRetry(t *testing.T) {
	fake := faketivo.New(t, testMAK, 30)
	fake.SlowDownloads(50 * time.Millisecond)
	e, out := downloader(t, t.TempDir(), fake)

	if n, err := e.QueueDownloads(tsTSN, []string{"1003", "1004"}, ""); err != nil || n != 2 {
		t.Fatalf("queue: %d, %v", n, err)
	}
	// Wait for the first to start, then cancel both: one under way, one
	// still waiting. A queued download is cancelled at once, so the same
	// request is refused only while it is waiting.
	deadline := time.Now().Add(10 * time.Second)
	for e.Transfers()[0].Bytes == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := e.QueueDownloads(tsTSN, []string{"1004"}, ""); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("queueing a waiting recording again: %v", err)
	}
	jobs := e.Transfers()
	for _, j := range jobs {
		if err := e.CancelTransfer(j.ID); err != nil {
			t.Fatal(err)
		}
	}
	jobs = waitTransfers(t, e)
	for _, j := range jobs {
		if j.State != store.Cancelled || j.Path != "" {
			t.Errorf("after cancelling: %+v", j)
		}
	}
	if left, _ := os.ReadDir(out); len(left) != 0 {
		t.Errorf("cancelling left %d files behind", len(left))
	}
	if err := e.CancelTransfer(9999); !errors.Is(err, ErrUnknownTransfer) {
		t.Errorf("unknown transfer: %v", err)
	}

	// A failure is reported, and a retry succeeds.
	fake.SlowDownloads(0)
	fake.TruncateDownloads(1)
	if err := e.RetryTransfer(jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	jobs = waitTransfers(t, e)
	if jobs[0].State != store.Failed || !strings.Contains(jobs[0].Error, "cut short") {
		t.Errorf("a broken download: %+v", jobs[0])
	}
	if left, _ := os.ReadDir(out); len(left) != 0 {
		t.Errorf("a failed download left %d files behind", len(left))
	}
	e.RetryTransfer(jobs[0].ID)
	jobs = waitTransfers(t, e)
	if jobs[0].State != store.Done || jobs[1].State != store.Cancelled {
		t.Errorf("after retrying: %s, %s", jobs[0].State, jobs[1].State)
	}
}

func TestDamagedDownloads(t *testing.T) {
	fake := faketivo.New(t, testMAK, 30)
	e, _ := downloader(t, t.TempDir(), fake)

	// By default a damaged recording is kept and the damage is reported.
	fake.CorruptDownloads(1)
	e.QueueDownloads(tsTSN, []string{"1003"}, "")
	jobs := waitTransfers(t, e)
	if jobs[0].State != store.Done || jobs[0].SyncLosses != 3 {
		t.Errorf("damaged, kept: %+v", jobs[0])
	}

	// Told to reject damage, it tries again as often as allowed.
	cfg := e.Config()
	cfg.Server["togo_ts_error_mode"] = "reject"
	cfg.Server["togo_ts_max_retries"] = "2"
	e.SaveConfig(cfg)

	fake.CorruptDownloads(2)
	e.QueueDownloads(tsTSN, []string{"1004"}, "")
	jobs = waitTransfers(t, e)
	if jobs[1].State != store.Done || jobs[1].Attempts != 3 || jobs[1].SyncLosses != 0 {
		t.Errorf("damaged twice, then clean: %+v", jobs[1])
	}

	fake.CorruptDownloads(5)
	e.QueueDownloads(tsTSN, []string{"1005"}, "")
	jobs = waitTransfers(t, e)
	if jobs[2].State != store.Failed || jobs[2].Attempts != 3 || !strings.Contains(jobs[2].Error, "damaged") {
		t.Errorf("always damaged: %+v", jobs[2])
	}
}

// The queue and the Now Playing list are still there after a restart, and
// a download that was under way starts again.
func TestQueueSurvivesRestart(t *testing.T) {
	fake := faketivo.New(t, testMAK, 30)
	fake.SlowDownloads(50 * time.Millisecond)
	dir := t.TempDir()
	mem := secrets.NewMemory()
	mem.Set(secrets.MAK, testMAK)

	first, err := New(Options{ConfigPath: filepath.Join(dir, "config.toml"), Secrets: mem})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "recordings")
	cfg := first.Config()
	cfg.Server["togo_path"] = out
	first.SaveConfig(cfg)
	addFake(first, fake, tsTSN)
	first.RefreshNowPlaying(tsTSN)
	waitLoaded(t, first, tsTSN)
	if n, err := first.QueueDownloads(tsTSN, []string{"1003", "1004"}, ""); err != nil || n != 2 {
		t.Fatalf("queue: %d, %v", n, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for first.Transfers()[0].Bytes == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	first.Close() // in the middle of the first download

	if left, _ := os.ReadDir(out); len(left) != 0 {
		t.Errorf("closing left %d partial files behind", len(left))
	}

	fake.SlowDownloads(0)
	second := newEngine(t, dir, mem)
	if np := second.NowPlaying(tsTSN); len(np.Recordings) != 30 || np.Fetched.IsZero() || np.Loading {
		t.Errorf("the saved list did not come back: %+v, %d recordings", np.NowPlayingStatus, len(np.Recordings))
	}
	jobs := second.Transfers()
	if len(jobs) != 2 || jobs[0].State != store.Queued || jobs[1].State != store.Queued || jobs[0].Attempts != 0 {
		t.Fatalf("the queue did not come back as it was: %+v", jobs)
	}

	// Nothing moves until the TiVo is found again; then both download.
	time.Sleep(50 * time.Millisecond)
	if second.Transfers()[0].State != store.Queued {
		t.Error("a download started before its TiVo was found")
	}
	addFake(second, fake, tsTSN)
	jobs = waitTransfers(t, second)
	if jobs[0].State != store.Done || jobs[1].State != store.Done {
		t.Errorf("after the restart: %s (%s), %s (%s)", jobs[0].State, jobs[0].Error, jobs[1].State, jobs[1].Error)
	}
}

// By default a recording is decrypted as it downloads and saved as an
// ordinary video file. The fake TiVo's recordings are not encrypted, so
// what comes out is the video packets without the .TiVo header.
func TestDownloadDecrypts(t *testing.T) {
	fake := faketivo.New(t, testMAK, 30)
	e, out := downloader(t, t.TempDir(), fake)
	cfg := e.Config()
	delete(cfg.Server, "togo_decrypt")
	e.SaveConfig(cfg)

	if n, err := e.QueueDownloads(tsTSN, []string{"1003"}, ""); err != nil || n != 1 {
		t.Fatalf("queue: %d, %v", n, err)
	}
	if n, err := e.QueueDownloads(tsTSN, []string{"1004"}, "ps"); err != nil || n != 1 {
		t.Fatalf("queue: %d, %v", n, err)
	}
	jobs := waitTransfers(t, e)
	for i, ts := range []bool{true, false} {
		j := jobs[i]
		ext := map[bool]string{true: ".ts", false: ".mpg"}[ts]
		if j.State != store.Done || !j.Decrypt || filepath.Ext(j.Path) != ext || strings.Contains(j.Path, "(TS)") {
			t.Errorf("transfer: %+v", j)
		}
		got, err := os.ReadFile(j.Path)
		want := faketivo.Recording(4000, ts)[1024:]
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: %v; %d bytes, want %d", filepath.Base(j.Path), err, len(got), len(want))
		}
		if _, err := os.Stat(j.Path + ".txt"); err != nil {
			t.Errorf("no details file beside %s", filepath.Base(j.Path))
		}
	}
	if left, _ := os.ReadDir(out); len(left) != 4 {
		t.Errorf("%d files in the folder, want two videos and two details files", len(left))
	}

	// A download that breaks leaves nothing behind when decrypting, too.
	fake.TruncateDownloads(1)
	e.QueueDownloads(tsTSN, []string{"1005"}, "")
	jobs = waitTransfers(t, e)
	if jobs[2].State != store.Failed || !strings.Contains(jobs[2].Error, "cut short") {
		t.Errorf("a broken download: %+v", jobs[2])
	}
	if left, _ := os.ReadDir(out); len(left) != 4 {
		t.Errorf("a failed download left files behind: %d in the folder", len(left))
	}
}
