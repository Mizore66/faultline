//go:build ignore

// Command gen builds internal/collation/root.bin from ICU's root collation
// source data, so Go compares strings the way Node 22's ICU does.
//
// Inputs (pass paths with -fractional and -unicodedata):
//   - FractionalUCA.txt from ICU 78.2
//     https://raw.githubusercontent.com/unicode-org/icu/release-78.2/icu4c/source/data/unidata/FractionalUCA.txt
//   - UnicodeData.txt from Unicode 17.0.0 (canonical combining classes)
//     https://www.unicode.org/Public/17.0.0/ucd/UnicodeData.txt
//
// The committed root.bin was built from inputs with these SHA-256 digests:
//
//	FractionalUCA.txt (ICU 78.1 and 78.2 are identical) d7cdfab860bf94c6f470c1fae39b81619a12f3a51f80d7cad1ba08404dec3de6
//	UnicodeData.txt 17.0.0                                2e1efc1dcb59c575eedf5ccae60f95229f706ee6d031835247d843c11d96470c
//
// Run from the repository root:
//
//	go run internal/collation/gen/main.go -fractional FractionalUCA.txt -unicodedata UnicodeData.txt
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
)

type ce struct {
	p    uint32 // left-justified primary bytes
	s, t uint16 // left-justified secondary and tertiary bytes
	han  rune   // nonzero: primary of this Han character
}

type mapping struct {
	prefix []rune
	key    []rune
	ces    []ce
}

func parseHex(s string) rune {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		log.Fatalf("bad hex %q: %v", s, err)
	}
	return rune(v)
}

func parseBytes(field string, width int) uint32 {
	field = strings.TrimSpace(field)
	if field == "" {
		return 0
	}
	parts := strings.Fields(field)
	if len(parts) > width {
		log.Fatalf("weight %q longer than %d bytes", field, width)
	}
	var v uint32
	for i, p := range parts {
		b, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			log.Fatalf("bad weight byte %q", p)
		}
		v |= uint32(b) << (8 * (width - 1 - i))
	}
	return v
}

func parseCEs(s string) []ce {
	var out []ce
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimSpace(s) {
		if s[0] != '[' {
			log.Fatalf("bad CE list %q", s)
		}
		end := strings.IndexByte(s, ']')
		body := s[1:end]
		s = s[end+1:]
		fields := strings.Split(body, ",")
		if strings.HasPrefix(strings.TrimSpace(fields[0]), "U+") {
			c := ce{han: parseHex(strings.TrimPrefix(strings.TrimSpace(fields[0]), "U+")), s: 0x0500, t: 0x0500}
			switch len(fields) {
			case 1:
			case 2:
				c.t = uint16(parseBytes(fields[1], 2))
			case 3:
				c.s = uint16(parseBytes(fields[1], 2))
				c.t = uint16(parseBytes(fields[2], 2))
			default:
				log.Fatalf("bad reference CE %q", body)
			}
			out = append(out, c)
			continue
		}
		if len(fields) != 3 {
			log.Fatalf("bad CE %q", body)
		}
		out = append(out, ce{p: parseBytes(fields[0], 4), s: uint16(parseBytes(fields[1], 2)), t: uint16(parseBytes(fields[2], 2))})
	}
	return out
}

func parseRunes(s string) []rune {
	var out []rune
	for _, f := range strings.Fields(s) {
		out = append(out, parseHex(f))
	}
	return out
}

func digest(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func main() {
	fractional := flag.String("fractional", "", "path to ICU FractionalUCA.txt")
	unicodeData := flag.String("unicodedata", "", "path to UnicodeData.txt")
	out := flag.String("out", "internal/collation/root.bin", "output path")
	flag.Parse()
	if *fractional == "" || *unicodeData == "" {
		log.Fatal("need -fractional and -unicodedata")
	}
	fmt.Println("FractionalUCA.txt sha256", digest(*fractional))
	fmt.Println("UnicodeData.txt  sha256", digest(*unicodeData))

	// Canonical combining classes and canonical decompositions.
	var cccRunes []rune
	cccOf := map[rune]byte{}
	decomp := map[rune][]rune{}
	ud, err := os.Open(*unicodeData)
	if err != nil {
		log.Fatal(err)
	}
	sc := bufio.NewScanner(ud)
	for sc.Scan() {
		f := strings.Split(sc.Text(), ";")
		if f[5] != "" && !strings.HasPrefix(f[5], "<") {
			decomp[parseHex(f[0])] = parseRunes(f[5])
		}
		ccc, _ := strconv.Atoi(f[3])
		if ccc != 0 {
			r := parseHex(f[0])
			cccRunes = append(cccRunes, r)
			cccOf[r] = byte(ccc)
		}
	}
	ud.Close()

	// Full (recursive) canonical decompositions; Hangul stays algorithmic.
	var full func(r rune) []rune
	full = func(r rune) []rune {
		d, ok := decomp[r]
		if !ok {
			return []rune{r}
		}
		var out []rune
		for _, x := range d {
			out = append(out, full(x)...)
		}
		return out
	}
	var decompRunes []rune
	for r := range decomp {
		decompRunes = append(decompRunes, r)
	}
	sort.Slice(decompRunes, func(i, j int) bool { return decompRunes[i] < decompRunes[j] })

	// Mappings and the radical-stroke Han order.
	var maps []mapping
	var han []rune
	hanSeen := map[rune]bool{}
	fu, err := os.Open(*fractional)
	if err != nil {
		log.Fatal(err)
	}
	sc = bufio.NewScanner(fu)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "[radical ") && !strings.HasPrefix(line, "[radical end") {
			body := line[strings.IndexByte(line, ':')+1 : strings.LastIndexByte(line, ']')]
			rs := []rune(body)
			for i := 0; i < len(rs); i++ {
				lo := rs[i]
				hi := lo
				if i+2 < len(rs) && rs[i+1] == '-' {
					hi = rs[i+2]
					i += 2
				}
				for r := lo; r <= hi; r++ {
					if hanSeen[r] {
						log.Fatalf("Han %U listed twice", r)
					}
					hanSeen[r] = true
					han = append(han, r)
				}
			}
			continue
		}
		if line == "" || line[0] == '#' || line[0] == '[' {
			continue
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		semi := strings.IndexByte(line, ';')
		if semi < 0 {
			continue
		}
		left, right := line[:semi], line[semi+1:]
		m := mapping{ces: parseCEs(right)}
		if bar := strings.IndexByte(left, '|'); bar >= 0 {
			m.prefix = parseRunes(left[:bar])
			m.key = parseRunes(left[bar+1:])
		} else {
			m.key = parseRunes(left)
		}
		maps = append(maps, m)
	}
	fu.Close()

	hanRank := map[rune]uint32{}
	for i, r := range han {
		hanRank[r] = uint32(i + 1)
	}
	for mi := range maps {
		for ci, c := range maps[mi].ces {
			if c.han != 0 {
				rank, ok := hanRank[c.han]
				if !ok {
					log.Fatalf("reference to non-Han %U", c.han)
				}
				maps[mi].ces[ci].p = 0x81000000 | rank
				maps[mi].ces[ci].han = 0
			}
		}
	}

	// Serialize.
	var b []byte
	b = append(b, 'U', 'C', 'A', 3)
	b = binary.AppendUvarint(b, uint64(len(cccRunes)))
	sort.Slice(cccRunes, func(i, j int) bool { return cccRunes[i] < cccRunes[j] })
	prev := rune(0)
	for _, r := range cccRunes {
		b = binary.AppendUvarint(b, uint64(r-prev))
		b = append(b, cccOf[r])
		prev = r
	}
	b = binary.AppendUvarint(b, uint64(len(decompRunes)))
	prev = 0
	for _, r := range decompRunes {
		b = binary.AppendUvarint(b, uint64(r-prev))
		d := full(r)
		b = binary.AppendUvarint(b, uint64(len(d)))
		for _, x := range d {
			b = binary.AppendUvarint(b, uint64(x))
		}
		prev = r
	}
	// Han, sorted by code point, with its radical-stroke rank.
	byRune := append([]rune(nil), han...)
	sort.Slice(byRune, func(i, j int) bool { return byRune[i] < byRune[j] })
	b = binary.AppendUvarint(b, uint64(len(byRune)))
	prev = 0
	for _, r := range byRune {
		b = binary.AppendUvarint(b, uint64(r-prev))
		b = binary.AppendUvarint(b, uint64(hanRank[r]))
		prev = r
	}
	// Single code point mappings sorted by code point, then the rest.
	var singles, others []mapping
	for _, m := range maps {
		if len(m.prefix) == 0 && len(m.key) == 1 {
			singles = append(singles, m)
		} else {
			others = append(others, m)
		}
	}
	sort.Slice(singles, func(i, j int) bool { return singles[i].key[0] < singles[j].key[0] })
	appendCEs := func(ces []ce) {
		b = binary.AppendUvarint(b, uint64(len(ces)))
		for _, c := range ces {
			b = binary.AppendUvarint(b, uint64(c.p))
			b = binary.AppendUvarint(b, uint64(c.s))
			b = binary.AppendUvarint(b, uint64(c.t))
		}
	}
	b = binary.AppendUvarint(b, uint64(len(singles)))
	prev = 0
	for _, m := range singles {
		b = binary.AppendUvarint(b, uint64(m.key[0]-prev))
		prev = m.key[0]
		appendCEs(m.ces)
	}
	b = binary.AppendUvarint(b, uint64(len(others)))
	for _, m := range others {
		b = binary.AppendUvarint(b, uint64(len(m.prefix)))
		for _, r := range m.prefix {
			b = binary.AppendUvarint(b, uint64(r))
		}
		b = binary.AppendUvarint(b, uint64(len(m.key)))
		for _, r := range m.key {
			b = binary.AppendUvarint(b, uint64(r))
		}
		appendCEs(m.ces)
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s: %d bytes, %d ccc, %d decompositions, %d Han, %d mappings\n", *out, len(b), len(cccRunes), len(decompRunes), len(han), len(maps))
}
