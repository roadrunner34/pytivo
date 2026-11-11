// Package pytivoimport converts a pyTivo.conf file into a TivoDesk
// configuration.
package pytivoimport

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"tivodesk/internal/config"
)

// Result is what an import produced. Nothing is saved by this package: the
// caller decides what to do with the configuration and the keys.
type Result struct {
	Config *config.Config

	// MAK is the Media Access Key from [Server], if the file had one. It
	// belongs in the secret store, not in the configuration.
	MAK string
	// TiVoMAKs are keys set for individual TiVos, by TSN.
	TiVoMAKs map[string]string

	// Warnings explain, in plain language, what was skipped or changed.
	Warnings []string
}

const tivoPrefix = "_tivo_"

// Settings that only made sense for pyTivo. They are dropped on import.
var dropped = map[string]string{
	"tivodecode":            "TivoDesk decrypts recordings itself",
	"tivolibre":             "TivoDesk decrypts recordings itself",
	"tdcat":                 "TivoDesk reads recording details itself",
	"nosettings":            "the web settings page is replaced by the app",
	"vrd_prompt":            "VideoReDo integration is not part of TivoDesk",
	"vrd_decrypt_qsf":       "VideoReDo integration is not part of TivoDesk",
	"vrd_delete_on_success": "VideoReDo integration is not part of TivoDesk",
}

// isLoggingSection reports whether a section configures Python's logging.
func isLoggingSection(name string) bool {
	switch name {
	case "loggers", "handlers", "formatters":
		return true
	}
	for _, p := range []string{"logger_", "handler_", "formatter_"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// File imports the pyTivo.conf at path.
func File(path string) (*Result, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Bytes(raw), nil
}

// Bytes imports the contents of a pyTivo.conf.
func Bytes(raw []byte) *Result {
	sections, warnings := parseINI(decode(raw))
	res := &Result{Config: config.New(), TiVoMAKs: map[string]string{}, Warnings: warnings}
	warn := func(format string, args ...any) {
		res.Warnings = append(res.Warnings, fmt.Sprintf(format, args...))
	}

	// copySettings moves a section's options into dst, pulling out the MAK
	// and leaving out settings TivoDesk has no use for.
	copySettings := func(where string, src map[string]string, dst config.Settings) (mak string) {
		keys := make([]string, 0, len(src))
		for k := range src {
			keys = append(keys, k)
		}
		slices.Sort(keys) // so warnings come out in a stable order
		for _, k := range keys {
			v := src[k]
			switch reason, isDropped := dropped[k]; {
			case k == "tivo_mak":
				mak = v
			case isDropped:
				warn("%s: %q was not imported: %s", where, k, reason)
			default:
				dst[k] = v
			}
		}
		return mak
	}

	loggingSeen := false
	for _, s := range sections {
		switch {
		case s.name == "Server":
			res.MAK = copySettings("[Server]", s.options, res.Config.Server)

		case isLoggingSection(s.name):
			loggingSeen = true

		case strings.HasPrefix(s.name, tivoPrefix):
			id := s.name[len(tivoPrefix):]
			dst := config.Settings{}
			mak := copySettings("["+s.name+"]", s.options, dst)
			switch class := strings.ToUpper(id); class {
			case config.ClassSD, config.ClassHD, config.Class4K:
				res.Config.Class[class] = dst
				if mak != "" {
					warn("[%s]: a Media Access Key on a whole class of TiVos is not supported and was not imported", s.name)
				}
			default:
				if id == "" {
					warn("[%s]: ignored, because it names no TiVo", s.name)
					continue
				}
				res.Config.TiVo[id] = dst
				if mak != "" {
					res.TiVoMAKs[id] = mak
				}
			}

		default:
			typ := strings.ToLower(s.options["type"])
			switch typ {
			case config.ShareVideo, config.ShareMusic, config.SharePhoto:
				share := config.Settings{}
				for k, v := range s.options {
					share[k] = v
				}
				share["type"] = typ
				res.Config.Share[s.name] = share
				if share["path"] == "" {
					warn("[%s]: the share has no path; set one before using it", s.name)
				}
			case "":
				warn("[%s]: not imported, because it has no type and is not a known section", s.name)
			default:
				warn("[%s]: not imported, because TivoDesk has no %q shares", s.name, typ)
			}
		}
	}
	if loggingSeen {
		warn("Python logging settings were not imported; TivoDesk has its own log viewer")
	}

	// A TiVo's share list may name shares that were not imported.
	for tsn, s := range res.Config.TiVo {
		list, ok := s["shares"]
		if !ok {
			continue
		}
		for _, name := range strings.Split(list, ",") {
			name = strings.TrimSpace(name)
			if _, exists := res.Config.Share[name]; name != "" && !exists {
				warn("[%s%s]: its share list names %q, which is not an imported share", tivoPrefix, tsn, name)
			}
		}
	}

	for _, p := range res.Config.Validate() {
		if strings.HasPrefix(p.Section, "share.") {
			continue // already explained above
		}
		warn("%s: %q %s; the default will be used", p.Section, p.Name, p.Message)
	}
	return res
}
