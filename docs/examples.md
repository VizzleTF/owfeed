# Examples

## The simplest thing that works

A directory of files, laid out the way it should install:

```
pkg/root/
  www/luci-static/demo/style.css
  etc/config/demo
```

```yaml
version: 1
feed:
  name: demofeed
  url: https://feed.example.org
publish:
  - target: github-pages
packages:
  - name: luci-app-demo
    build: mkpkg
    arch: noarch
    version: 1.0.0-r1
    files: ./pkg/root
    depends: [luci-base]
    conffiles: ["/etc/config/demo"]
```

```sh
owfeed lock --update
owfeed build && owfeed sign && owfeed index && owfeed doctor
```

---

## A real LuCI theme: luci-theme-footstrap

[luci-theme-footstrap](https://github.com/VizzleTF/luci-theme-footstrap) is a noarch LuCI theme —
CSS, templates and translations, no compiled code. Exactly the case where the status quo asks for 35
SDK builds to produce the same bytes 35 times.

`owfeed build` packages a directory; it does not build one. So the work splits in two: your build
produces the rootfs, owfeed turns it into a signed feed.

### 1. Stage the rootfs

The source layout is LuCI's, and the mapping is `luci.mk`'s:

| in the repo | installs as |
|---|---|
| `htdocs/` | `/www` |
| `ucode/` | `/usr/share/ucode/luci` |
| `luasrc/` | `/usr/lib/lua/luci` |
| `root/` | `/` |
| `i18n/<lang>/*.po` | `/usr/lib/lua/luci/i18n/<name>.<lang>.lmo` |

```sh
#!/bin/sh
# stage.sh — produce dist/root, the directory owfeed packages.
set -e
SRC=luci-theme-footstrap
DIST=dist/root
rm -rf dist && mkdir -p "$DIST"

./"$SRC"/build-css.sh                       # minified CSS into htdocs/

mkdir -p "$DIST/www" "$DIST/usr/share/ucode/luci" "$DIST/usr/lib/lua/luci/i18n"
cp -a "$SRC"/htdocs/. "$DIST/www/"
cp -a "$SRC"/ucode/.  "$DIST/usr/share/ucode/luci/"
cp -a "$SRC"/root/.   "$DIST/"

# Translations. LuCI loads compiled .lmo catalogues; shipping the .po does nothing.
for po in "$SRC"/i18n/*/*.po; do
  lang=$(basename "$(dirname "$po")")
  po2lmo "$po" "$DIST/usr/lib/lua/luci/i18n/footstrap-theme.$lang.lmo"
done

git describe --tags --abbrev=0 | sed 's/^v//;s/$/-r1/' > dist/VERSION
```

Two details in there are not cosmetic:

- **`po2lmo` comes from `luci-base`'s host build**, not from the SDK tarball. owfeed does not run it
  for you, and that is deliberate — see below.
- **The catalogue is `footstrap-theme.<lang>.lmo`, not `footstrap.<lang>.lmo`.** `lmo_load_catalog`
  globs `*.<lang>.lmo`, so any basename loads. But a router upgrading from an older release still
  owns `footstrap.ru.lmo` through the separate `luci-i18n-footstrap-ru` package. Same path means a
  file conflict, and apk refuses the upgrade. The rename is what avoids it.

This is why owfeed will not compile `.po` for you: the basename is a packaging decision, not a
derivable one, and guessing it breaks exactly the case that motivated the rename. What owfeed does
instead is **refuse to package a `.po` file**, so pointing `files:` at the source tree fails loudly
rather than shipping a theme with no translations.

### 2. owfeed.yml

```yaml
version: 1

feed:
  name: footstrap
  url: https://feed.footstrap.dev
  title: Footstrap
  maintainer: "VizzleTF <vizzletf47@gmail.com>"
  license: Apache-2.0
  homepage: https://github.com/VizzleTF/luci-theme-footstrap

publish:
  - target: github-pages

packages:
  - name: luci-theme-footstrap
    build: mkpkg
    arch: noarch                          # never "all" — apk rejects it
    version-from: file:./dist/VERSION
    files: ./dist/root
    description: "A modern, fast LuCI theme."
    depends: [luci-base]
    conffiles: ["/etc/config/footstrap"]
```

`conffiles` is the entry worth reading twice. The theme ships `/etc/config/footstrap`; leaving it
undeclared means sysupgrade replaces the user's settings with the package defaults on every firmware
upgrade, silently. `owfeed doctor` reports it as OWF207.

### 3. Build the feed

```sh
./stage.sh
owfeed lock --update
owfeed build && owfeed sign && owfeed index && owfeed doctor
```

The theme's `/etc/uci-defaults/30_luci-theme-footstrap` runs on install without any extra
configuration: owfeed wraps `post-install` the way `package-pack.mk` does, so `default_postinst`
applies uci-defaults and enables init scripts. A bare post-install script would install the files and
do none of that.

### 4. What your users run

```sh
owfeed install-snippet
```

Paste the output into the README verbatim. `doctor` checks it has not drifted.

---

## Two packages from one repository

Each entry is independent; they share the feed, the key and the index. Only the `packages:` section
is shown here — the rest of `owfeed.yml` is unchanged.

```yaml
packages:
  - name: luci-theme-footstrap
    build: mkpkg
    arch: noarch
    version-from: file:./dist/VERSION
    files: ./dist/root
    depends: [luci-base]
    conffiles: ["/etc/config/footstrap"]

  - name: luci-app-footstrap-tools
    build: mkpkg
    arch: noarch
    version: 1.0.0-r1
    files: ./tools/dist/root
    depends: [luci-base, luci-theme-footstrap]
    scripts:
      post-install: ./tools/post-install.sh
```

A `scripts:` entry is merged into the OpenWrt wrapper rather than replacing it, so
`default_postinst` still runs.
