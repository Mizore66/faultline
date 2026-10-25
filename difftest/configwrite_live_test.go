//go:build faultline_test

package difftest

import (
	"encoding/base64"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

type cwStep struct {
	kind, alias, image string
	aliasSet, imageSet bool
}

func (s cwStep) json() jsjson.Value {
	o := jsjson.NewObj()
	o.Set("kind", jsjson.MakeString(s.kind))
	if s.aliasSet {
		o.Set("alias", jsjson.MakeString(s.alias))
	}
	if s.imageSet {
		o.Set("image", jsjson.MakeString(s.image))
	}
	return jsjson.MakeObject(o)
}

// cwFileState is "mode:base64(bytes)" with dir masked, or "nofile".
func cwFileState(dir string) string {
	file := filepath.Join(dir, ".faultline", "config.json")
	info, err := os.Stat(file)
	if err != nil {
		return "nofile"
	}
	data, _ := os.ReadFile(file)
	text := strings.ReplaceAll(string(data), dir, "<D>")
	return fmt.Sprintf("%o:%s", info.Mode().Perm(), base64.StdEncoding.EncodeToString([]byte(text)))
}

func TestLiveConfigWriters(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(20, 1))
	dg := func(b byte) string { return "sha256:" + strings.Repeat(string(b), 64) }
	images := []string{"ghcr.io/x/y@" + dg('a'), "node@" + dg('b'), "a:5000/b@" + dg('c'), "a:5000/b:1@" + dg('d'), "x", "n@sha256:" + strings.Repeat("g", 64)}
	aliases := []string{"node", "python", "go", "ruby", ""}
	for i := 0; i < 500; i++ {
		goDir, nodeDir := t.TempDir(), t.TempDir()
		switch r.IntN(3) {
		case 0:
		default:
			// Seed through ensure so both sides start from identical valid config.
			img := images[r.IntN(3)]
			body := `{"schemaVersion":"faultline.config.v1","snapshot":{"trackedFilesOnly":true},"stores":{"incidents":"i","witnesses":"w","recordings":"r"}`
			if r.IntN(2) == 0 {
				body += `,"image":"` + img + `","runtime":{"alias":"go","image":"` + img + `"}`
			}
			body += "}"
			for _, d := range []string{goDir, nodeDir} {
				os.Mkdir(filepath.Join(d, ".faultline"), 0o755)
				os.WriteFile(filepath.Join(d, ".faultline", "config.json"), []byte(body), 0o644)
			}
		}
		var steps []cwStep
		for n := 1 + r.IntN(4); n > 0; n-- {
			s := cwStep{kind: []string{"ensure", "update"}[r.IntN(2)]}
			s.image = images[r.IntN(len(images))]
			s.imageSet = s.kind == "update" || r.IntN(2) == 0
			if s.kind == "update" && r.IntN(8) == 0 {
				s.image = images[r.IntN(3)]
			}
			s.alias = aliases[r.IntN(len(aliases))]
			s.aliasSet = r.IntN(2) == 0
			// The Go API uses "" for absent, so "" is never sent as a value.
			s.aliasSet = s.aliasSet && s.alias != ""
			s.imageSet = s.imageSet && s.image != ""
			steps = append(steps, s)
		}
		var items []jsjson.Value
		var want []string
		for _, s := range steps {
			items = append(items, s.json())
			var head string
			var err error
			alias, image := "", ""
			if s.aliasSet {
				alias = s.alias
			}
			if s.imageSet {
				image = s.image
			}
			if s.kind == "ensure" {
				var res gitinv.EnsureConfigResult
				res, err = gitinv.EnsureConfig(gitinv.EnsureConfigOptions{Repository: goDir, RuntimeAlias: alias, Image: image})
				head = res.Status
			} else {
				_, err = gitinv.UpdateConfigImage(goDir, image, alias)
				head = "OK"
			}
			if err != nil {
				head = "THREW:" + strings.ReplaceAll(jsexc.Message(err), goDir, "<D>")
			}
			want = append(want, head+"|"+cwFileState(goDir))
		}
		args := `{"dir":` + jsjson.Quote(nodeDir) + `,"steps":` + jsjson.Stringify(jsjson.MakeArray(items)) + "}"
		liveCheck(t, c, "configWriters", args, strings.Join(want, "\n"))
	}
}

func TestLiveRuntimeMappingWriter(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(20, 2))
	fps := []string{"sha256:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("b", 64), "sha256:" + strings.Repeat("A", 64), "sha256:zz", "", "mapping"}
	imgs := []string{"node@sha256:" + strings.Repeat("c", 64), "ghcr.io/x/y@sha256:" + strings.Repeat("d", 64), "node", "n@sha256:" + strings.Repeat("E", 64), "-x@sha256:" + strings.Repeat("e", 64), ""}
	pick := func(list []string) string {
		if r.IntN(5) == 0 {
			return list[r.IntN(len(list))]
		}
		return list[r.IntN(2)]
	}
	for i := 0; i < 1000; i++ {
		var pairs []gitinv.MappingEntry
		var items []jsjson.Value
		for n := r.IntN(5); n > 0; n-- {
			e := gitinv.MappingEntry{Fingerprint: pick(fps), Image: pick(imgs)}
			pairs = append(pairs, e)
			o := jsjson.NewObj()
			o.Set("fingerprintDigest", jsjson.MakeString(e.Fingerprint))
			o.Set("image", jsjson.MakeString(e.Image))
			items = append(items, jsjson.MakeObject(o))
		}
		source := ""
		if r.IntN(3) == 0 {
			source = "custom source"
		}
		goDir, nodeDir := t.TempDir(), t.TempDir()
		res, err := gitinv.WriteRuntimeMapping(gitinv.RuntimeMappingWrite{Pairs: pairs, OutputPath: filepath.Join(goDir, "sub", "m.json"), GeneratedAt: "2026-01-02T03:04:05.000Z", Source: source})
		var got string
		if err != nil {
			got = "THREW:" + jsexc.Message(err)
		} else {
			info, _ := os.Stat(res.Path)
			data, _ := os.ReadFile(res.Path)
			got = fmt.Sprintf("%o:%s", info.Mode().Perm(), data)
		}
		args := `{"dir":` + jsjson.Quote(nodeDir) + `,"pairs":` + jsjson.Stringify(jsjson.MakeArray(items)) + `,"generatedAt":"2026-01-02T03:04:05.000Z"`
		if source != "" {
			args += `,"source":"custom source"`
		}
		liveCheck(t, c, "runtimeMappingWriter", args+"}", got)
	}
}
