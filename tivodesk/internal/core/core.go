// Package core is TivoDesk's headless engine. Everything the app does goes
// through it; the desktop window and the command-line tool are thin shells
// on top.
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"sync"

	"tivodesk/internal/config"
	"tivodesk/internal/events"
	"tivodesk/internal/logx"
	"tivodesk/internal/pytivoimport"
	"tivodesk/internal/secrets"
	"tivodesk/internal/store"
	"tivodesk/internal/tivo"
)

// Version is the application version shown in the UI.
const Version = "0.1.0-dev"

// API is everything a shell may ask of the engine. The desktop UI uses it
// in-process today; it is an interface so that a later service mode can put
// the same calls behind IPC without changing the UI.
type API interface {
	Info() Info
	Config() *config.Config
	SaveConfig(c *config.Config) ([]config.Problem, error)
	Problems() []config.Problem

	HasMAK() bool
	SetMAK(mak string) error
	ClearMAK() error

	PreviewImport(path string) (*ImportPreview, error)
	ApplyImport(path string) (*ImportPreview, error)

	TiVos() []tivo.Box
	NowPlaying(tsn string) NowPlaying
	RefreshNowPlaying(tsn string) error

	Transfers() []store.Job
	QueueDownloads(tsn string, ids []string, format string) (int, error)
	CancelTransfer(id int64) error
	RetryTransfer(id int64) error
	ClearTransfers() int

	Logs() []logx.Entry
	Events() *events.Bus
}

// Info describes the running engine.
type Info struct {
	Version    string `json:"version"`
	GoVersion  string `json:"goVersion"`
	ConfigPath string `json:"configPath"`
}

// ImportPreview is the outcome of reading a pyTivo.conf, without the keys
// themselves: the UI only needs to know whether there were any.
type ImportPreview struct {
	Config   *config.Config `json:"config"`
	HasMAK   bool           `json:"hasMAK"`
	TiVoMAKs int            `json:"tivoMAKs"` // how many TiVos have their own key
	Warnings []string       `json:"warnings"`
}

// ErrInvalidMAK reports a Media Access Key that is not ten digits.
var ErrInvalidMAK = errors.New("the Media Access Key must be exactly 10 digits")

// Options configure an Engine.
type Options struct {
	ConfigPath string // where the configuration is stored
	// DataPath is where the download queue and saved lists are kept. If
	// empty, it is "tivodesk.db" beside the configuration.
	DataPath string
	Secrets  secrets.Store // where keys are stored
	Log      *logx.Log     // optional; one is created if nil

	// Discover finds TiVos and records them in the registry until its
	// context is cancelled. It is discovery.Browse in the app, and nil in
	// tests, which add TiVos by hand.
	Discover func(ctx context.Context, reg *tivo.Registry, log *slog.Logger)
}

// Engine is the running application, minus any window.
type Engine struct {
	path    string
	secrets secrets.Store
	log     *logx.Log
	logger  *slog.Logger
	bus     events.Bus

	mu  sync.RWMutex
	cfg *config.Config

	tivos tivo.Registry

	// Background work stops when stop is called; done waits for it.
	ctx  context.Context
	stop context.CancelFunc
	done sync.WaitGroup

	nplMu sync.Mutex
	npl   map[string]*nowPlaying // by TSN

	store *store.Store

	clientMu sync.Mutex
	clients  map[string]*cachedClient // by TSN

	jobMu   sync.Mutex
	jobs    []*store.Job                      // the download queue, oldest first
	workers map[string]bool                   // TiVos with a download worker running
	cancels map[int64]context.CancelCauseFunc // stops the download under way, by job
}

var _ API = (*Engine)(nil)

// New loads the configuration and returns an engine ready to use.
func New(opts Options) (*Engine, error) {
	if opts.Log == nil {
		opts.Log = logx.New(2000, nil)
	}
	e := &Engine{
		path:    opts.ConfigPath,
		secrets: opts.Secrets,
		log:     opts.Log,
		logger:  opts.Log.Logger(),
	}
	e.log.OnEntry(func(entry logx.Entry) { e.bus.Publish(events.LogEntry, entry) })

	// Register stored keys with the log before anything can log them.
	if mak, err := e.secrets.Get(secrets.MAK); err == nil {
		e.log.Hide(mak)
	} else if !errors.Is(err, secrets.ErrNotFound) {
		e.logger.Warn("could not read the credential store", "err", err)
	}

	cfg, err := config.Load(e.path)
	if err != nil {
		return nil, err
	}
	for tsn := range cfg.TiVo {
		if mak, err := e.secrets.Get(secrets.TiVoMAK(tsn)); err == nil {
			e.log.Hide(mak)
		}
	}
	e.cfg = cfg
	e.applyLogLevel(cfg)

	// The GUID identifies this server to TiVos, so it is created once and
	// saved straight away.
	if _, had := cfg.Server["guid"]; !had {
		cfg.GUID()
		if err := cfg.Save(e.path); err != nil {
			e.logger.Warn("could not save the configuration", "path", e.path, "err", err)
		}
	}

	if opts.DataPath == "" {
		opts.DataPath = filepath.Join(filepath.Dir(e.path), "tivodesk.db")
	}
	if e.store, err = store.Open(opts.DataPath); err != nil {
		return nil, err
	}

	e.ctx, e.stop = context.WithCancel(context.Background())
	e.npl = map[string]*nowPlaying{}
	e.clients = map[string]*cachedClient{}
	e.workers = map[string]bool{}
	e.cancels = map[int64]context.CancelCauseFunc{}
	e.loadListings()
	e.loadJobs()
	e.tivos.OnChange(func() {
		e.bus.Publish(events.TiVosChanged, nil)
		e.kickAll() // a TiVo that just appeared may have downloads waiting
	})
	if opts.Discover != nil {
		e.done.Add(1)
		go func() {
			defer e.done.Done()
			opts.Discover(e.ctx, &e.tivos, e.logger)
		}()
	}

	e.logger.Info("TivoDesk engine ready", "version", Version, "config", e.path,
		"shares", len(cfg.Share), "mak", e.HasMAK())
	for _, p := range cfg.Validate() {
		e.logger.Warn("setting will not work as written", "section", p.Section, "name", p.Name, "problem", p.Message)
	}
	return e, nil
}

func (e *Engine) applyLogLevel(c *config.Config) {
	if c.ServerBool("debug", false) {
		e.log.SetLevel(slog.LevelDebug)
	} else {
		e.log.SetLevel(slog.LevelInfo)
	}
}

// Close shuts the engine down.
func (e *Engine) Close() {
	e.stop()
	e.done.Wait()
	if err := e.store.Close(); err != nil {
		e.logger.Warn("could not close the database", "err", err)
	}
	e.logger.Info("TivoDesk engine stopped")
}

// Logger returns the engine's logger, for shells that want to log too.
func (e *Engine) Logger() *slog.Logger { return e.logger }

func (e *Engine) Info() Info {
	return Info{Version: Version, GoVersion: runtime.Version(), ConfigPath: e.path}
}

// Config returns a copy of the current configuration.
func (e *Engine) Config() *config.Config {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg.Clone()
}

// Problems returns the current settings that will not work as written.
func (e *Engine) Problems() []config.Problem {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg.Validate()
}

// SaveConfig replaces the configuration and writes it to disk. It returns
// the settings that will not work as written; those do not stop the save,
// because each falls back to its default.
func (e *Engine) SaveConfig(c *config.Config) ([]config.Problem, error) {
	if c == nil {
		return nil, errors.New("no configuration given")
	}
	c = c.Clone()
	if c.Class == nil || c.TiVo == nil || c.Share == nil || c.Server == nil {
		return nil, errors.New("incomplete configuration")
	}

	e.mu.Lock()
	// The GUID is the server's identity; the UI cannot change or drop it.
	if guid := e.cfg.Server["guid"]; guid != "" {
		c.Server["guid"] = guid
	} else {
		c.GUID()
	}
	if err := c.Save(e.path); err != nil {
		e.mu.Unlock()
		return nil, fmt.Errorf("could not save the configuration: %w", err)
	}
	e.cfg = c
	e.mu.Unlock()

	e.applyLogLevel(c)
	problems := c.Validate()
	e.logger.Info("configuration saved", "shares", len(c.Share), "problems", len(problems))
	e.bus.Publish(events.ConfigChanged, nil)
	return problems, nil
}

// HasMAK reports whether a Media Access Key is stored.
func (e *Engine) HasMAK() bool {
	_, err := e.secrets.Get(secrets.MAK)
	return err == nil
}

// SetMAK stores the Media Access Key.
func (e *Engine) SetMAK(mak string) error {
	if !secrets.ValidMAK(mak) {
		return ErrInvalidMAK
	}
	e.log.Hide(mak)
	if err := e.secrets.Set(secrets.MAK, mak); err != nil {
		return fmt.Errorf("could not store the Media Access Key: %w", err)
	}
	e.logger.Info("Media Access Key stored")
	e.bus.Publish(events.MAKChanged, true)
	e.kickAll() // downloads may have been waiting for the key
	return nil
}

// ClearMAK removes the stored Media Access Key.
func (e *Engine) ClearMAK() error {
	if err := e.secrets.Delete(secrets.MAK); err != nil {
		return fmt.Errorf("could not remove the Media Access Key: %w", err)
	}
	e.logger.Info("Media Access Key removed")
	e.bus.Publish(events.MAKChanged, false)
	return nil
}

func preview(res *pytivoimport.Result) *ImportPreview {
	warnings := res.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	return &ImportPreview{
		Config:   res.Config,
		HasMAK:   res.MAK != "",
		TiVoMAKs: len(res.TiVoMAKs),
		Warnings: warnings,
	}
}

// PreviewImport reads a pyTivo.conf and reports what importing it would
// do, without changing anything.
func (e *Engine) PreviewImport(path string) (*ImportPreview, error) {
	res, err := pytivoimport.File(path)
	if err != nil {
		return nil, err
	}
	// The keys are about to pass through memory; keep them out of the log.
	e.hideImported(res)
	return preview(res), nil
}

func (e *Engine) hideImported(res *pytivoimport.Result) {
	e.log.Hide(res.MAK)
	for _, mak := range res.TiVoMAKs {
		e.log.Hide(mak)
	}
}

// ApplyImport replaces the configuration with the contents of a pyTivo.conf
// and stores any Media Access Keys it holds.
func (e *Engine) ApplyImport(path string) (*ImportPreview, error) {
	res, err := pytivoimport.File(path)
	if err != nil {
		return nil, err
	}
	e.hideImported(res)
	out := preview(res)

	if res.MAK != "" {
		if secrets.ValidMAK(res.MAK) {
			if err := e.SetMAK(res.MAK); err != nil {
				return nil, err
			}
		} else {
			out.HasMAK = false
			out.Warnings = append(out.Warnings, "[Server]: the Media Access Key was not imported, because it is not 10 digits")
		}
	}
	for tsn, mak := range res.TiVoMAKs {
		if err := e.secrets.Set(secrets.TiVoMAK(tsn), mak); err != nil {
			return nil, fmt.Errorf("could not store the key for TiVo %s: %w", tsn, err)
		}
	}
	if _, err := e.SaveConfig(res.Config); err != nil {
		return nil, err
	}
	e.logger.Info("imported pyTivo configuration", "path", path, "shares", len(res.Config.Share),
		"tivos", len(res.Config.TiVo), "warnings", len(out.Warnings))
	out.Config = e.Config()
	return out, nil
}

// Logs returns the recent log entries, oldest first.
func (e *Engine) Logs() []logx.Entry { return e.log.Entries() }

// Events returns the bus the engine publishes on.
func (e *Engine) Events() *events.Bus { return &e.bus }
