package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"tivodesk/internal/config"
	"tivodesk/internal/events"
	"tivodesk/internal/store"
	"tivodesk/internal/tivo"
	"tivodesk/internal/togo"
)

var (
	// ErrNoFolder means downloads have nowhere to go yet.
	ErrNoFolder = errors.New("choose a recordings folder in Settings first")
	// ErrUnknownTransfer means no transfer has that ID.
	ErrUnknownTransfer = errors.New("that transfer is no longer in the list")

	// errUserCancelled is the reason given to a download the user stops.
	errUserCancelled = errors.New("cancelled")
)

// TransferProgress is the payload of the transfer.progress event.
type TransferProgress struct {
	ID         int64   `json:"id"`
	Bytes      int64   `json:"bytes"`
	Rate       float64 `json:"rate"` // bytes per second
	SyncLosses int     `json:"syncLosses"`
}

// cachedClient is a TiVo client and what it was built for.
type cachedClient struct {
	key    string
	client *togo.Client
}

// clientFor returns the client for a TiVo, keeping one per box so that
// requests reuse its connection and its sign-in. A changed address or key
// gets a new one.
func (e *Engine) clientFor(box tivo.Box, mak string) *togo.Client {
	key := net.JoinHostPort(box.Address, strconv.Itoa(box.Port)) + "|" + mak
	e.clientMu.Lock()
	defer e.clientMu.Unlock()
	if c := e.clients[box.TSN]; c != nil && c.key == key {
		return c.client
	}
	c := &cachedClient{key: key, client: togo.NewClient(box.Address, box.Port, mak)}
	e.clients[box.TSN] = c
	return c.client
}

// downloadDir returns the folder recordings are saved in. The togo_path
// setting is a folder, or the name of a share whose folder is used.
func (e *Engine) downloadDir(cfg *config.Config) (string, error) {
	dir := strings.TrimSpace(cfg.Server["togo_path"])
	if share, ok := cfg.Share[dir]; ok {
		dir = strings.TrimSpace(share["path"])
	}
	if dir == "" {
		return "", ErrNoFolder
	}
	return dir, nil
}

// loadJobs reads the queue left by the last run. A download that was under
// way then starts again from the beginning: a TiVo cannot resume one.
func (e *Engine) loadJobs() {
	jobs, err := e.store.Jobs()
	if err != nil {
		e.logger.Warn("could not read the download queue", "err", err)
		return
	}
	for _, j := range jobs {
		if j.State == store.Running {
			if j.Path != "" {
				os.Remove(j.Path + togo.PartSuffix)
			}
			j.State, j.Bytes, j.Path = store.Queued, 0, ""
			e.saveJob(j)
		}
	}
	e.jobs = jobs
}

// saveJob writes a job to the database. A failure is logged, not returned:
// the job carries on in memory.
func (e *Engine) saveJob(j *store.Job) {
	if err := e.store.UpdateJob(j); err != nil {
		e.logger.Warn("could not save the download queue", "err", err)
	}
}

// Transfers returns every download, oldest first.
func (e *Engine) Transfers() []store.Job {
	e.jobMu.Lock()
	defer e.jobMu.Unlock()
	out := make([]store.Job, len(e.jobs))
	for i, j := range e.jobs {
		out[i] = *j
	}
	return out
}

// QueueDownloads adds recordings from a TiVo's Now Playing list to the
// download queue and returns how many were added. Recordings that cannot
// be downloaded (copy protected, or still recording) and ones already
// waiting are left out.
//
// format is "ts" or "ps" to force a transport or program stream, or empty
// to use a transport stream wherever the TiVo offers one.
func (e *Engine) QueueDownloads(tsn string, ids []string, format string) (int, error) {
	box, ok := e.tivos.Get(tsn)
	if !ok {
		return 0, ErrUnknownTiVo
	}
	if _, err := e.makFor(tsn); err != nil {
		return 0, err
	}
	if _, err := e.downloadDir(e.Config()); err != nil {
		return 0, err
	}
	ts := config.IsTSCapable(tsn)
	switch strings.ToLower(format) {
	case "", "ts":
	case "ps":
		ts = false
	default:
		return 0, fmt.Errorf("unknown format %q", format)
	}

	e.nplMu.Lock()
	byID := map[string]togo.Recording{}
	if st := e.npl[tsn]; st != nil && st.listing != nil {
		for _, r := range st.listing.Recordings {
			byID[r.ID] = r
		}
	}
	e.nplMu.Unlock()

	var protected, recording, waiting, missing int
	added := 0
	e.jobMu.Lock()
	for _, id := range ids {
		r, ok := byID[id]
		switch {
		case !ok:
			missing++
			continue
		case r.CopyProtected:
			protected++
			continue
		case r.InProgress:
			recording++
			continue
		}
		active := false
		for _, j := range e.jobs {
			if j.TSN == tsn && j.Recording.ID == id && !j.IsFinished() {
				active = true
				break
			}
		}
		if active {
			waiting++
			continue
		}
		job := &store.Job{
			TSN: tsn, TiVoName: box.Name, Recording: r, TS: ts,
			State: store.Queued, Created: time.Now(),
		}
		if err := e.store.AddJob(job); err != nil {
			e.jobMu.Unlock()
			return added, fmt.Errorf("could not save the download queue: %w", err)
		}
		e.jobs = append(e.jobs, job)
		added++
		e.logger.Info("queued for download", "tivo", box.Name, "title", r.Title, "episode", r.EpisodeTitle)
	}
	e.jobMu.Unlock()

	if added == 0 {
		switch {
		case protected > 0:
			return 0, errors.New("copy-protected recordings cannot be downloaded")
		case recording > 0:
			return 0, errors.New("that recording is still being recorded; download it when it finishes")
		case waiting > 0:
			return 0, errors.New("already in the download queue")
		case missing > 0:
			return 0, errors.New("that recording is no longer on the TiVo; refresh the list")
		}
		return 0, nil
	}
	e.bus.Publish(events.TransfersChanged, nil)
	e.kick(tsn)
	return added, nil
}

// CancelTransfer stops a download that is waiting or under way.
func (e *Engine) CancelTransfer(id int64) error {
	e.jobMu.Lock()
	j := e.job(id)
	if j == nil {
		e.jobMu.Unlock()
		return ErrUnknownTransfer
	}
	switch j.State {
	case store.Queued:
		j.State, j.Finished = store.Cancelled, time.Now()
		e.saveJob(j)
	case store.Running:
		// The worker notices, removes the partial file and marks the job.
		if cancel := e.cancels[id]; cancel != nil {
			cancel(errUserCancelled)
		}
	}
	e.jobMu.Unlock()
	e.bus.Publish(events.TransfersChanged, nil)
	return nil
}

// RetryTransfer puts a failed or cancelled download back in the queue.
func (e *Engine) RetryTransfer(id int64) error {
	e.jobMu.Lock()
	j := e.job(id)
	if j == nil {
		e.jobMu.Unlock()
		return ErrUnknownTransfer
	}
	if j.State != store.Failed && j.State != store.Cancelled {
		e.jobMu.Unlock()
		return nil
	}
	j.State, j.Error, j.Bytes, j.SyncLosses, j.Attempts = store.Queued, "", 0, 0, 0
	j.Finished = time.Time{}
	e.saveJob(j)
	tsn := j.TSN
	e.jobMu.Unlock()
	e.bus.Publish(events.TransfersChanged, nil)
	e.kick(tsn)
	return nil
}

// ClearTransfers removes finished, failed and cancelled downloads from the
// list and returns how many it removed. The files stay where they are.
func (e *Engine) ClearTransfers() int {
	e.jobMu.Lock()
	kept := e.jobs[:0:0]
	removed := 0
	for _, j := range e.jobs {
		if !j.IsFinished() {
			kept = append(kept, j)
			continue
		}
		if err := e.store.DeleteJob(j.ID); err != nil {
			e.logger.Warn("could not save the download queue", "err", err)
		}
		removed++
	}
	e.jobs = kept
	e.jobMu.Unlock()
	if removed > 0 {
		e.bus.Publish(events.TransfersChanged, nil)
	}
	return removed
}

// job finds a job by ID. The caller holds jobMu.
func (e *Engine) job(id int64) *store.Job {
	for _, j := range e.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

// kickAll starts a worker for every TiVo with downloads waiting. It is
// called when something a waiting download needs may have arrived: a TiVo
// coming online, or a key being stored.
func (e *Engine) kickAll() {
	e.jobMu.Lock()
	waiting := map[string]bool{}
	for _, j := range e.jobs {
		if j.State == store.Queued {
			waiting[j.TSN] = true
		}
	}
	e.jobMu.Unlock()
	for tsn := range waiting {
		e.kick(tsn)
	}
}

// kick starts the worker for a TiVo unless one is already running. Each
// TiVo has one worker, because a TiVo sends one recording at a time;
// different TiVos download side by side.
func (e *Engine) kick(tsn string) {
	if box, ok := e.tivos.Get(tsn); !ok || !box.Online {
		return // discovery will kick again when the TiVo appears
	}
	if _, err := e.makFor(tsn); err != nil {
		return // storing a key will kick again
	}
	e.jobMu.Lock()
	defer e.jobMu.Unlock()
	if e.workers[tsn] || e.ctx.Err() != nil {
		return
	}
	e.workers[tsn] = true
	e.done.Add(1)
	go e.work(tsn)
}

// work downloads a TiVo's queued recordings one after another, and returns
// when none are left.
func (e *Engine) work(tsn string) {
	defer e.done.Done()
	for {
		e.jobMu.Lock()
		var job *store.Job
		for _, j := range e.jobs {
			if j.TSN == tsn && j.State == store.Queued {
				job = j
				break
			}
		}
		if job == nil || e.ctx.Err() != nil {
			delete(e.workers, tsn)
			e.jobMu.Unlock()
			return
		}
		ctx, cancel := context.WithCancelCause(e.ctx)
		e.cancels[job.ID] = cancel
		job.State, job.Started, job.Finished = store.Running, time.Now(), time.Time{}
		job.Error, job.Bytes, job.SyncLosses, job.Rate = "", 0, 0, 0
		job.Attempts++
		e.saveJob(job)
		e.jobMu.Unlock()
		e.bus.Publish(events.TransfersChanged, nil)

		wait := e.download(ctx, job)
		cancel(nil)

		e.jobMu.Lock()
		delete(e.cancels, job.ID)
		job.Rate = 0
		if job.IsFinished() {
			job.Finished = time.Now()
		}
		e.saveJob(job)
		if wait {
			delete(e.workers, tsn)
		}
		e.jobMu.Unlock()
		e.bus.Publish(events.TransfersChanged, nil)
		if wait {
			return
		}
	}
}

// download fetches one recording and leaves the job in its new state. It
// returns true if the job went back in the queue to wait for something
// that is missing, such as the TiVo, in which case the worker should stop.
func (e *Engine) download(ctx context.Context, job *store.Job) (wait bool) {
	// set changes the job under the lock.
	set := func(change func()) {
		e.jobMu.Lock()
		change()
		e.jobMu.Unlock()
	}
	requeue := func() {
		set(func() {
			job.State, job.Bytes, job.Path = store.Queued, 0, ""
			job.Attempts--
		})
	}
	fail := func(err error) {
		e.logger.Warn("download failed", "tivo", job.TiVoName, "title", job.Recording.Title, "err", err)
		set(func() { job.State, job.Error, job.Path = store.Failed, err.Error(), "" })
	}

	box, ok := e.tivos.Get(job.TSN)
	mak, err := e.makFor(job.TSN)
	if !ok || !box.Online || err != nil {
		requeue()
		return true
	}
	e.log.Hide(mak)
	client := e.clientFor(box, mak)

	cfg := e.Config()
	dir, err := e.downloadDir(cfg)
	if err != nil {
		fail(err)
		return false
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(fmt.Errorf("could not create the recordings folder: %w", err))
		return false
	}
	path := togo.FreePath(dir, job.Recording, job.TS, cfg.ServerBool("togo_sortable_names", false), time.Local)
	part := path + togo.PartSuffix
	f, err := os.Create(part)
	if err != nil {
		fail(fmt.Errorf("could not create the file: %w", err))
		return false
	}
	set(func() { job.Path = path })
	e.logger.Info("download started", "tivo", box.Name, "file", path)

	// Progress reaches the UI twice a second at most.
	start := time.Now()
	lastTime, lastBytes := start, int64(0)
	res, err := client.Download(ctx, job.Recording.ContentURL, f, togo.DownloadOptions{
		TS: job.TS,
		Progress: func(written int64, losses int) {
			now := time.Now()
			since := now.Sub(lastTime)
			e.jobMu.Lock()
			job.Bytes, job.SyncLosses = written, losses
			if since < 500*time.Millisecond {
				e.jobMu.Unlock()
				return
			}
			job.Rate = float64(written-lastBytes) / since.Seconds()
			p := TransferProgress{ID: job.ID, Bytes: written, Rate: job.Rate, SyncLosses: losses}
			e.jobMu.Unlock()
			lastTime, lastBytes = now, written
			e.bus.Publish(events.TransferProgress, p)
		},
	})
	if closeErr := f.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("could not finish writing the file: %w", closeErr)
	}

	if err != nil {
		os.Remove(part)
		switch {
		case errors.Is(context.Cause(ctx), errUserCancelled):
			e.logger.Info("download cancelled", "tivo", box.Name, "title", job.Recording.Title)
			set(func() { job.State, job.Path = store.Cancelled, "" })
		case e.ctx.Err() != nil:
			// TivoDesk is closing; the download starts again next time.
			requeue()
			return true
		default:
			fail(err)
		}
		return false
	}

	// A transport stream with damaged packets is kept, unless the settings
	// say to reject it; then it is fetched again, up to a limit.
	if res.SyncLosses > 0 {
		e.logger.Warn("download has damaged packets", "file", path, "packets", res.SyncLosses)
		if strings.EqualFold(cfg.ServerValue("togo_ts_error_mode", "ignore"), "reject") {
			os.Remove(part)
			retries, _ := strconv.Atoi(cfg.ServerValue("togo_ts_max_retries", "0"))
			if job.Attempts <= retries {
				set(func() { job.State, job.Bytes, job.Path = store.Queued, 0, "" })
				return false
			}
			fail(fmt.Errorf("the recording arrived with %d damaged packets", res.SyncLosses))
			return false
		}
	}

	if err := os.Rename(part, path); err != nil {
		os.Remove(part)
		fail(fmt.Errorf("could not name the finished file: %w", err))
		return false
	}
	if cfg.ServerBool("togo_save_txt", true) {
		e.saveDetails(client, job.Recording, path)
	}

	took := time.Since(start)
	e.logger.Info("download finished", "tivo", box.Name, "file", path, "bytes", res.Bytes,
		"took", took.Round(time.Second), "MB/s", fmt.Sprintf("%.1f", float64(res.Bytes)/1e6/max(took.Seconds(), 0.001)))
	set(func() {
		job.State, job.Bytes, job.SyncLosses = store.Done, res.Bytes, res.SyncLosses
	})
	return false
}

// saveDetails writes a recording's details beside it, as "<file>.txt", in
// the format pyTivo and TivoDesk read when serving the file back. If the
// TiVo will not give the full details, what the list said is saved.
func (e *Engine) saveDetails(client *togo.Client, r togo.Recording, path string) {
	ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
	defer cancel()
	meta, err := client.Details(ctx, r)
	if err != nil {
		e.logger.Warn("could not read the recording's full details; saving the basics", "file", path, "err", err)
	}
	f, err := os.Create(path + ".txt")
	if err == nil {
		err = meta.Write(f)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		e.logger.Warn("could not save the recording's details", "file", path+".txt", "err", err)
	}
}
