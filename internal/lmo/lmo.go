// Package lmo compiles gettext catalogues into the LMO format LuCI reads.
//
// LuCI loads compiled .lmo catalogues at runtime and ignores .po entirely, so a
// package that ships the sources installs cleanly and has no translations. The
// compiler that produces them, po2lmo, is a host tool built from luci-base — it is
// not in the SDK tarball, and requiring it would put a C build of the LuCI feed in
// front of anyone packaging a theme.
//
// This is a port of modules/luci-base/src/po2lmo.c and the sfh_hash in lib/lmo.c.
// The output is byte-identical to that tool's, which is what the golden test in
// this package asserts against a real 12 KB catalogue — a translation table that is
// merely close is a table that returns the wrong string.
package lmo

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// entry is one record of the trailing index.
type entry struct {
	keyID  uint32
	valID  uint32
	offset uint32
	length uint32
}

// Compile turns the contents of a .po file into a .lmo.
//
// It returns nil when the catalogue holds nothing translatable, matching po2lmo,
// which deletes its output file in that case rather than writing an empty one.
func Compile(r io.Reader) ([]byte, error) {
	c := &compiler{}
	if err := c.parse(r); err != nil {
		return nil, err
	}
	if c.offset == 0 {
		return nil, nil
	}

	// The index is sorted by key so lookups can binary-search it. po2lmo uses an
	// unstable qsort; sorting stably here only makes ties deterministic.
	sort.SliceStable(c.entries, func(i, j int) bool { return c.entries[i].keyID < c.entries[j].keyID })

	out := c.data
	var buf [4]byte
	for _, e := range c.entries {
		for _, v := range []uint32{e.keyID, e.valID, e.offset, e.length} {
			binary.BigEndian.PutUint32(buf[:], v)
			out = append(out, buf[:]...)
		}
	}
	// The trailer is the size of the data section, which is how a reader finds
	// where the index begins.
	binary.BigEndian.PutUint32(buf[:], c.offset)
	return append(out, buf[:]...), nil
}

type compiler struct {
	data    []byte
	entries []entry
	offset  uint32

	// The message being accumulated. A .po entry spans several lines, and any of
	// its fields may continue across them.
	ctxt      string
	id        string
	idPlural  string
	val       [10]string
	valSet    [10]bool
	pluralNum int
	// cur points at the field the next continuation line appends to.
	cur *string
}

func (c *compiler) parse(r io.Reader) error {
	c.pluralNum = -1

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for {
		line := ""
		eof := !sc.Scan()
		if !eof {
			line = sc.Text()
		}

		switch {
		case strings.HasPrefix(line, `msgctxt "`):
			c.flush()
			c.ctxt = ""
			c.cur = &c.ctxt
		case eof, strings.HasPrefix(line, `msgid "`):
			c.flush()
			c.id = ""
			c.cur = &c.id
		case strings.HasPrefix(line, `msgid_plural "`):
			c.idPlural = ""
			c.cur = &c.idPlural
		case strings.HasPrefix(line, `msgstr "`), strings.HasPrefix(line, "msgstr["):
			n := 0
			if len(line) > 6 && line[6] == '[' {
				var err error
				n, err = strconv.Atoi(strings.SplitN(line[7:], "]", 2)[0])
				if err != nil {
					return fmt.Errorf("malformed plural index in %q", line)
				}
			}
			if n >= len(c.val) {
				return fmt.Errorf("too many plural forms: msgstr[%d]", n)
			}
			c.pluralNum = n
			c.val[n], c.valSet[n] = "", false
			c.cur = &c.val[n]
		}

		if eof {
			break
		}
		if c.cur != nil {
			if s, ok := extractString(line); ok && s != "" {
				*c.cur += s
				// Track which plural slots were actually written to, since an empty
				// translation is not the same as an absent one.
				for i := range c.val {
					if c.cur == &c.val[i] {
						c.valSet[i] = true
					}
				}
			}
		}
	}
	return sc.Err()
}

// flush emits the accumulated message, if there is one.
//
// When there is nothing to emit it must leave the state alone rather than clear it.
// A .po entry is written as `msgctxt` then `msgid`, and both lines run through here:
// clearing on the second would throw away the context that the first had just read,
// and every contextual translation would be filed under the wrong key.
func (c *compiler) flush() {
	if c.id == "" && !c.valSet[0] {
		return
	}
	defer c.reset()

	switch {
	case c.id != "" && c.valSet[0]:
		for i := 0; i <= c.pluralNum && i < len(c.val); i++ {
			if !c.valSet[i] {
				continue
			}
			key := c.key(i)
			keyID := sfhHash([]byte(key), uint32(len(key)))
			valID := sfhHash([]byte(c.val[i]), uint32(len(c.val[i])))
			// po2lmo drops an entry whose key and value hash alike, which is how an
			// untranslated string — msgstr identical to msgid — costs nothing.
			if keyID == valID {
				continue
			}
			c.appendEntry(keyID, uint32(c.pluralNum+1), c.val[i])
		}

	case c.valSet[0]:
		// No msgid: this is the catalogue header, and the only field taken from it
		// is the plural formula, keyed on zero so a reader can find it.
		if formula, ok := pluralForms(c.val[0]); ok {
			c.appendEntry(0, 0, formula)
		}
	}
}

// appendEntry writes a value into the data section and records where it went.
// Values are padded to a multiple of four so every entry starts aligned.
func (c *compiler) appendEntry(keyID, valID uint32, value string) {
	c.entries = append(c.entries, entry{
		keyID: keyID, valID: valID, offset: c.offset, length: uint32(len(value)),
	})
	c.data = append(c.data, value...)
	pad := (4 - (len(value) % 4)) % 4
	for i := 0; i < pad; i++ {
		c.data = append(c.data, 0)
	}
	c.offset += uint32(len(value) + pad)
}

// key builds the lookup key, which folds the context and the plural form into the
// message id with the separators LuCI's runtime expects.
func (c *compiler) key(i int) string {
	switch {
	case c.ctxt != "" && c.idPlural != "":
		return c.ctxt + "\x01" + c.id + "\x02" + strconv.Itoa(i)
	case c.ctxt != "":
		return c.ctxt + "\x01" + c.id
	case c.idPlural != "":
		return c.id + "\x02" + strconv.Itoa(i)
	default:
		return c.id
	}
}

func (c *compiler) reset() {
	c.ctxt, c.id, c.idPlural = "", "", ""
	c.val = [10]string{}
	c.valSet = [10]bool{}
	c.pluralNum = -1
	c.cur = nil
}

// pluralForms finds the Plural-Forms field in a catalogue header.
//
// The header arrives as one string in which the line breaks are still the literal
// two characters backslash and n, because extractString leaves escapes other than
// \" and \\ alone.
func pluralForms(header string) (string, bool) {
	const want = "plural-forms: "
	for _, field := range strings.Split(header, `\n`) {
		if len(field) >= len(want) && strings.EqualFold(field[:len(want)], want) {
			return field[len(want):], true
		}
	}
	return "", false
}

// extractString pulls the quoted payload out of a .po line.
//
// Only \" and \\ are unescaped; everything else, \n included, stays as the two
// characters it was written as. That is what po2lmo does, and the header parser
// above depends on it.
func extractString(line string) (string, bool) {
	if strings.HasPrefix(line, "#") {
		return "", false
	}
	start := strings.IndexByte(line, '"')
	if start < 0 {
		return "", false
	}

	var b strings.Builder
	esc := false
	for i := start + 1; i < len(line); i++ {
		ch := line[i]
		switch {
		case esc:
			esc = false
			if ch == '"' || ch == '\\' {
				// Replace the backslash already written with the character itself.
				s := b.String()
				b.Reset()
				b.WriteString(s[:len(s)-1])
			}
			b.WriteByte(ch)
		case ch == '\\':
			b.WriteByte(ch)
			esc = true
		case ch == '"':
			return b.String(), true
		default:
			b.WriteByte(ch)
		}
	}
	// An unterminated string. po2lmo reads whatever is left in its buffer here;
	// returning what was found is the sane reading of a malformed line.
	return b.String(), true
}

// sfhHash is Paul Hsieh's SuperFastHash, seeded with the length, exactly as
// lib/lmo.c implements it. The values it produces are the file format.
func sfhHash(data []byte, init uint32) uint32 {
	if len(data) == 0 {
		return 0
	}
	hash := init
	rem := len(data) & 3
	i := 0

	for n := len(data) >> 2; n > 0; n-- {
		hash += get16(data[i:])
		tmp := (get16(data[i+2:]) << 11) ^ hash
		hash = (hash << 16) ^ tmp
		i += 4
		hash += hash >> 11
	}

	switch rem {
	case 3:
		hash += get16(data[i:])
		hash ^= hash << 16
		hash ^= uint32(int32(int8(data[i+2])) << 18)
		hash += hash >> 11
	case 2:
		hash += get16(data[i:])
		hash ^= hash << 11
		hash += hash >> 17
	case 1:
		hash += uint32(int32(int8(data[i])))
		hash ^= hash << 10
		hash += hash >> 1
	}

	hash ^= hash << 3
	hash += hash >> 5
	hash ^= hash << 4
	hash += hash >> 17
	hash ^= hash << 25
	hash += hash >> 6
	return hash
}

func get16(d []byte) uint32 { return uint32(d[0]) | uint32(d[1])<<8 }
