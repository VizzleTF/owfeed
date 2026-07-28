package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/VizzleTF/owfeed/internal/config"
	"github.com/VizzleTF/owfeed/internal/ipk"
)

// buildIPK produces the 24.10 container from the same staged rootfs the apk path
// uses. The two lines differ in more than packaging: opkg calls an
// architecture-independent package "all" where apk requires "noarch" and rejects
// "all", so the same package carries a different architecture name per artifact.
func buildIPK(req Request, arch, outDir string) (*Result, error) {
	p := req.Package
	name := p.EffectiveName()

	payload, err := os.MkdirTemp(outDir, ".owfeed-ipk-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(payload)

	files := ExpandArch(p.Files, arch)
	if err := copyTree(filepath.Join(req.Root, files), payload, req.SourceDateEpoch); err != nil {
		return nil, fmt.Errorf("%s: staging %s: %w", name, files, err)
	}
	catalogues, err := compileCatalogues(payload, req.Root, p.I18n, req.SourceDateEpoch)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	pkgArch := arch
	if arch == config.Noarch {
		pkgArch = ipk.ArchAll
	}

	out, err := ipk.Build(ipk.Package{
		Name:        name,
		Version:     req.Version,
		Arch:        pkgArch,
		Description: p.Description,
		License:     firstNonEmpty(p.License, req.Feed.License),
		Maintainer:  firstNonEmpty(p.Maintainer, req.Feed.Maintainer),
		URL:         firstNonEmpty(p.URL, req.Feed.Homepage),
		Depends:     p.Depends,
		Provides:    provides(p),
		Conflicts:   p.Conflicts,
		Conffiles:   p.Conffiles,
		Scripts:     ipkScripts(name, p, req.Root),
	}, ipk.Options{Payload: payload, OutDir: outDir, Epoch: req.SourceDateEpoch})
	if err != nil {
		return nil, err
	}

	notes := []string{"built for the 24.10 line, which is opkg rather than apk"}
	if len(catalogues) > 0 {
		notes = append(notes, fmt.Sprintf("compiled %d translation catalogue(s)", len(catalogues)))
	}
	return &Result{Name: name, Version: req.Version, Arch: pkgArch, File: out, Notes: notes, Payload: catalogues}, nil
}

// ipkScripts wraps the lifecycle scripts the way OpenWrt's ipk path does. The
// wrapper is the same idea as the apk one and a different name: opkg calls them
// postinst and prerm, and default_postinst is what runs a package's uci-defaults
// and enables its init scripts.
func ipkScripts(name string, p config.Package, root string) map[string]string {
	body := func(kind string) string {
		path, ok := p.Scripts[kind]
		if !ok {
			return ""
		}
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return ""
		}
		return stripShebang(string(b))
	}

	return map[string]string{
		"postinst": strings.Join([]string{
			"#!/bin/sh",
			`[ "${IPKG_NO_SCRIPT}" = "1" ] && exit 0`,
			`[ -s "${IPKG_INSTROOT}/lib/functions.sh" ] || exit 0`,
			`. ${IPKG_INSTROOT}/lib/functions.sh`,
			`default_postinst $0 $@`,
			body("post-install"),
		}, "\n"),
		"prerm": strings.Join([]string{
			"#!/bin/sh",
			`[ -s "${IPKG_INSTROOT}/lib/functions.sh" ] || exit 0`,
			`. ${IPKG_INSTROOT}/lib/functions.sh`,
			`default_prerm $0 $@`,
			body("pre-deinstall"),
		}, "\n"),
	}
}
