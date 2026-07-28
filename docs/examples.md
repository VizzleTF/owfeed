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

Translations are not in the table because owfeed compiles those itself — see step 2.

```sh
#!/bin/sh
# stage.sh — produce dist/root, the directory owfeed packages.
set -e
SRC=luci-theme-footstrap
DIST=dist/root
rm -rf dist && mkdir -p "$DIST"

./"$SRC"/build-css.sh                       # minified CSS into htdocs/

mkdir -p "$DIST/www" "$DIST/usr/share/ucode/luci"
cp -a "$SRC"/htdocs/. "$DIST/www/"
cp -a "$SRC"/ucode/.  "$DIST/usr/share/ucode/luci/"
cp -a "$SRC"/root/.   "$DIST/"

git describe --tags --abbrev=0 | sed 's/^v//;s/$/-r1/' > dist/VERSION
```

No `po2lmo` call, and no need for one: it is a host tool built from `luci-base`, so requiring it
would put a C build of the LuCI feed in front of anyone packaging a theme. owfeed has its own
compiler, byte-identical to that tool's output.

Sources never go in the payload. Point `files:` at a source tree and owfeed refuses it by name —
`.po`, `.scss`, `node_modules`, `.DS_Store` — rather than shipping a package that installs cleanly
and is missing what the tree implied.

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
    i18n:
      from: ./luci-theme-footstrap/i18n     # a directory of <lang>/*.po
      basename: footstrap-theme             # -> footstrap-theme.<lang>.lmo
```

Two entries are worth reading twice.

**`conffiles`.** The theme ships `/etc/config/footstrap`; leaving it undeclared means sysupgrade
replaces the user's settings with the package defaults on every firmware upgrade, silently.
`owfeed doctor` reports it as OWF207.

**`i18n.basename`.** It defaults to the `.po` file's own name, which is what `luci.mk` does — here
that would be `footstrap.<lang>.lmo`. Footstrap sets it to `footstrap-theme` instead, and the reason
is worth knowing before you pick your own. LuCI's loader globs `*.<lang>.lmo`, so any basename is
found. But this theme used to ship its translations through separate `luci-i18n-footstrap-<lang>`
packages, and a router upgrading from that release still owns `footstrap.ru.lmo`. Reusing the path
is a file conflict, and apk refuses the upgrade — the one it was supposed to deliver. If your
package never shipped a `luci-i18n-*` variant, the default is fine.

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
