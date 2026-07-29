package build

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"owfeed.org/owfeed/internal/config"
	"owfeed.org/owfeed/internal/lmo"
)

// compileCatalogues turns a package's .po files into the .lmo files LuCI reads and
// writes them into the payload.
//
// This runs before the sidecars are generated, so the catalogues appear in the
// package's own file list exactly as an SDK build's would.
func compileCatalogues(payload, root string, spec *config.I18n, epoch time.Time) ([]string, error) {
	if spec == nil {
		return nil, nil
	}

	dest := spec.Dest
	if dest == "" {
		dest = config.DefaultI18nDest
	}
	outDir := filepath.Join(payload, filepath.FromSlash(strings.TrimPrefix(dest, "/")))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	// <lang>/*.po is the layout both LuCI's po/ and the i18n/ variant use. A
	// templates/ directory holding .pot files is skipped by the glob rather than by
	// a special case, which is also how the Makefiles that use this layout skip it.
	from := filepath.Join(root, spec.From)
	matches, err := filepath.Glob(filepath.Join(from, "*", "*.po"))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("i18n.from %s contains no <lang>/*.po files", spec.From)
	}
	sort.Strings(matches)

	var written []string
	for _, po := range matches {
		lang := filepath.Base(filepath.Dir(po))
		base := spec.Basename
		if base == "" {
			base = strings.TrimSuffix(filepath.Base(po), ".po")
		}

		f, err := os.Open(po)
		if err != nil {
			return nil, err
		}
		data, err := lmo.Compile(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", po, err)
		}
		if data == nil {
			// Nothing translated. po2lmo removes its output in this case, and an
			// empty catalogue on the router would be a file that means nothing.
			continue
		}

		out := filepath.Join(outDir, fmt.Sprintf("%s.%s.lmo", base, lang))
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return nil, err
		}
		if err := stamp(out, epoch); err != nil {
			return nil, err
		}
		written = append(written, fmt.Sprintf("%s/%s.%s.lmo", strings.TrimSuffix(dest, "/"), base, lang))
	}

	if len(written) == 0 {
		return nil, fmt.Errorf("i18n.from %s has .po files but none carry a translation", spec.From)
	}
	return written, nil
}
