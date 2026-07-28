# Примеры

*[English version](examples.md)*

Четыре разобранных примера, от простого к сложному. Каждый — это каталог, который у вас уже есть,
конфиг и четыре команды. Если форма фида ещё не сложилась в голове,
[Как это устроено](../README_ru.md#как-это-устроено) — двадцать строк.

- [Самое простое, что работает](#самое-простое-что-работает)
- [Тема LuCI: luci-theme-footstrap](#тема-luci-luci-theme-footstrap) — переводы
- [Два пакета из одного репозитория: podkop](#два-пакета-из-одного-репозитория-podkop) — конфликты,
  реальные зависимости
- [Скомпилированный бинарь: podkop-updater](#скомпилированный-бинарь-podkop-updater) — несколько архитектур

---

## Самое простое, что работает

Каталог файлов, разложенный так, как он должен установиться:

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

## Тема LuCI: luci-theme-footstrap

[luci-theme-footstrap](https://github.com/VizzleTF/luci-theme-footstrap) — noarch-тема LuCI: CSS,
шаблоны и переводы, ничего компилируемого. Ровно тот случай, где статус-кво требует 35 SDK-сборок,
чтобы 35 раз получить одни и те же байты.

`owfeed build` пакует каталог, а не собирает его. Поэтому работа делится надвое: ваша сборка делает
rootfs, owfeed превращает его в подписанный фид.

### 1. Подготовить rootfs

Раскладка исходников — LuCI-шная, соответствие — из `luci.mk`:

| в репозитории | ставится как |
|---|---|
| `htdocs/` | `/www` |
| `ucode/` | `/usr/share/ucode/luci` |
| `luasrc/` | `/usr/lib/lua/luci` |
| `root/` | `/` |
| `i18n/<lang>/*.po` | `/usr/lib/lua/luci/i18n/<name>.<lang>.lmo` |

Переводов в таблице нет, потому что их owfeed компилирует сам — см. шаг 2.

```sh
#!/bin/sh
# stage.sh — сделать dist/root, каталог, который упакует owfeed.
set -e
SRC=luci-theme-footstrap
DIST=dist/root
rm -rf dist && mkdir -p "$DIST"

./"$SRC"/build-css.sh                       # минифицированный CSS в htdocs/

mkdir -p "$DIST/www" "$DIST/usr/share/ucode/luci"
cp -a "$SRC"/htdocs/. "$DIST/www/"
cp -a "$SRC"/ucode/.  "$DIST/usr/share/ucode/luci/"
cp -a "$SRC"/root/.   "$DIST/"

git describe --tags --abbrev=0 | sed 's/^v//;s/$/-r1/' > dist/VERSION
```

Вызова `po2lmo` нет, и он не нужен: это host-утилита из `luci-base`, требовать её — значит ставить
C-сборку LuCI-фида перед каждым, кто пакует тему. У owfeed свой компилятор, побайтово совпадающий с
выводом оригинала.

Исходники в payload не попадают никогда. Направьте `files:` на дерево исходников — owfeed откажется
и назовёт файл: `.po`, `.scss`, `node_modules`, `.DS_Store`. Лучше так, чем пакет, который ставится
чисто и не содержит того, что дерево подразумевало.

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
    arch: noarch                          # никогда "all" — apk такое отвергает
    version-from: file:./dist/VERSION
    files: ./dist/root
    description: "A modern, fast LuCI theme."
    depends: [luci-base]
    conffiles: ["/etc/config/footstrap"]
    i18n:
      from: ./luci-theme-footstrap/i18n     # каталог с <lang>/*.po
      basename: footstrap-theme             # -> footstrap-theme.<lang>.lmo
```

Два поля стоит прочитать дважды.

**`conffiles`.** Тема шипает `/etc/config/footstrap`; не объявить его — значит, что sysupgrade при
каждом обновлении прошивки молча заменит настройки пользователя дефолтами пакета. `owfeed doctor`
сообщает это как OWF207.

**`i18n.basename`.** По умолчанию — имя самого `.po`-файла, как делает `luci.mk`; здесь это было бы
`footstrap.<lang>.lmo`. Footstrap ставит `footstrap-theme`, и причину стоит знать до того, как
выберете своё. Загрузчик LuCI ищет по маске `*.<lang>.lmo`, так что найдётся любое имя. Но раньше
эта тема шипала переводы отдельными пакетами `luci-i18n-footstrap-<lang>`, и роутер, обновляющийся
с того релиза, всё ещё владеет `footstrap.ru.lmo`. Занять тот же путь — конфликт файлов, и apk
откажет в том самом апгрейде. Если ваш пакет никогда не шипал вариант `luci-i18n-*`, дефолт годится.

### 3. Собрать фид

```sh
./stage.sh
owfeed lock --update
owfeed build && owfeed sign && owfeed index && owfeed doctor
```

`/etc/uci-defaults/30_luci-theme-footstrap` из темы отработает при установке без дополнительной
настройки: owfeed оборачивает `post-install` так же, как `package-pack.mk`, поэтому
`default_postinst` применяет uci-defaults и включает init-скрипты. Голый post-install-скрипт поставил
бы файлы и не сделал бы ничего из этого.

### 4. Что выполняют пользователи

```sh
owfeed install-snippet
```

Вывод вставляется в README дословно. `doctor` следит, чтобы он не разошёлся.

---

## Два пакета из одного репозитория: podkop

[podkop](https://github.com/itdoginfo/podkop) шипает из одного репозитория два пакета — сервис на
shell-скриптах и LuCI-приложение к нему. Оба `PKGARCH:=all` с пустым `Build/Compile`, так что
тулчейн не нужен ни одному. Это конфиг, который их собирает; проверен целиком на настоящем
апстримном репозитории.

```yaml
version: 1

feed:
  name: podkop
  url: https://feed.example.org
  title: podkop
  maintainer: "ITDog <podkop@itdog.info>"
  license: GPL-2.0-or-later
  homepage: https://podkop.net

publish:
  - target: github-pages

packages:
  - name: podkop
    build: mkpkg
    arch: noarch
    version-from: file:./VERSION
    files: ./staging/podkop
    description: "Domain routing. Use of VLESS, Shadowsocks technologies"
    depends: [sing-box, curl, jq, kmod-nft-tproxy, coreutils-base64, bind-dig]
    conflicts: [https-dns-proxy, nextdns, luci-app-passwall, luci-app-passwall2]
    conffiles: ["/etc/config/podkop"]

  - name: luci-app-podkop
    build: mkpkg
    arch: noarch
    version-from: file:./VERSION
    files: ./staging/luci-app-podkop
    description: "LuCI podkop app"
    depends: [luci-base, podkop]
    i18n:
      from: ./luci-app-podkop/po
      basename: podkop
```

Подготовка — обычное копирование плюс подстановка версии, которую делают Makefile'ы:

```sh
#!/bin/sh
set -e
VER="0.$(date +%d%m%Y)"; echo "$VER-r1" > VERSION

# luci-app-podkop: htdocs -> /www, root -> /
mkdir -p staging/luci-app-podkop/www
cp -a luci-app-podkop/htdocs/. staging/luci-app-podkop/www/
cp -a luci-app-podkop/root/.   staging/luci-app-podkop/

# podkop: files/ ложится 1:1, кроме usr/lib/* -> /usr/lib/podkop/
mkdir -p staging/podkop/usr/lib/podkop
cp -a podkop/files/etc podkop/files/usr/bin staging/podkop/
cp -a podkop/files/usr/lib/.                staging/podkop/usr/lib/podkop/

grep -rl __COMPILED_VERSION_VARIABLE__ staging | xargs sed -i "s/__COMPILED_VERSION_VARIABLE__/$VER/g"
```

```sh
owfeed lock --update
owfeed build && owfeed sign && owfeed index && owfeed doctor
#   built dist/noarch/podkop-0.28072026-r1.apk
#   built dist/noarch/luci-app-podkop-0.28072026-r1.apk
#     note: compiled 1 translation catalogue(s): /usr/lib/lua/luci/i18n/podkop.ru.lmo
#   25.12: 2 package(s) across 35 architecture(s)
#   390 checks passed
```

На роутере `apk add luci-app-podkop` вытягивает всю цепочку — `sing-box`, `curl`, `jq`,
`kmod-nft-tproxy`, `coreutils-base64`, `bind-dig` — из официальных фидов и ставится **без**
`--allow-untrusted`.

### `conflicts:` делает то, чего не умеет официальная сборка

Makefile podkop объявляет `CONFLICTS:=https-dns-proxy nextdns luci-app-passwall
luci-app-passwall2`, потому что все они переписывают таблицу маршрутизации. На 25.12 это объявление
не работает: `package-pack.mk` кладёт `Conflicts:` только в ipk-control и никогда не передаёт в
`mkpkg`, так что собранный apk-пакет не несёт ничего.

apk конфликты поддерживает — это зависимость с ведущим `!` — и owfeed их пишет:

```
ERROR: unable to select packages:
  https-dns-proxy-2026.05.06-r1:
    breaks: podkop-0.28072026-r1[!https-dns-proxy]
```

### Про `i18n.basename` здесь

podkop объявляет `LUCI_LANGUAGES:=en ru`, из-за чего `luci.mk` выпускает отдельные пакеты
`luci-i18n-podkop-<lang>`. Сложить каталоги внутрь `luci-app-podkop`, как делает конфиг выше,
означает, что роутер, поставивший языковой пакет с прошлого релиза, уже владеет
`/usr/lib/lua/luci/i18n/podkop.ru.lmo`. Либо продолжайте выпускать языковые пакеты, либо возьмите
basename, который не столкнётся, как сделала `luci-theme-footstrap`. owfeed за вас не угадает —
`doctor` тоже не видит чужой пакет.

---

## Скомпилированный бинарь: podkop-updater

Статическому Go-бинарю не нужен OpenWrt SDK — нужна сборка под правильный таргет, — поэтому
SDK-less путь не ограничен `noarch`. Один апстримный артефакт обычно покрывает несколько
OpenWrt-архитектур с общим GOARCH: одна сборка `arm64` закрывает все четыре `aarch64_*`.

```yaml
- name: podkop-updater
  build: mkpkg
  arch:
    - x86_64                 # GOARCH=amd64
    - aarch64_cortex-a53     # GOARCH=arm64, все четыре
    - aarch64_cortex-a72
    - aarch64_cortex-a76
    - aarch64_generic
    - mipsel_24kc            # GOARCH=mipsle, GOMIPS=softfloat
    - mipsel_74kc
  version-from: file:./staging/podkop-updater.version
  files: ./staging/podkop-updater/{arch}
  description: "Watches podkop releases and drives update and rollback from Telegram."
```

`{arch}` обязателен, как только архитектур больше одной. Две архитектуры не могут делить один
payload — если бы могли, пакет был бы `noarch`, — поэтому пропуск шаблона это ошибка, а не тихий
промах.

Сборка кладётся в `dist/<arch>/`, потому что apk выводит имя файла только из имени и версии: две
архитектуры одного пакета столкнулись бы в плоском каталоге. Индексация потом кладёт `noarch`-пакет
в каталог каждой архитектуры, а пер-архитектурный — только в свой.

Соответствие GOARCH → OpenWrt-архитектуры живёт в вашем fetch-скрипте, а не в owfeed: это свойство
вашего тулчейна, а не упаковки. Рабочий пример — в
[VizzleTF/owfeed-packages](https://github.com/VizzleTF/owfeed-packages), живом фиде, собранном
именно так.
