package difftest

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mizore66/faultline/difftest/gen"
	"github.com/Mizore66/faultline/difftest/oracle"
	"github.com/Mizore66/faultline/internal/gitinv"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
)

// threwOr renders a Go result like the oracle's: canonical JSON, or
// "THREW:<message>" for an error.
func threwOr(v jsjson.Value, err error) string {
	if err != nil {
		return "THREW:" + jsexc.Message(err)
	}
	return canonicalOf(v)
}

// liveOutcomes counts, per op, the results that were errors and the rest.
var liveOutcomes = map[string]*[2]int{}

func liveCheck(t *testing.T, c *oracle.Client, op, args, got string) {
	t.Helper()
	want := oracleText(t, c, op, args)
	n := liveOutcomes[op]
	if n == nil {
		n = &[2]int{}
		liveOutcomes[op] = n
		t.Cleanup(func() { t.Logf("%s: %d errors, %d results", op, n[0], n[1]) })
	}
	if strings.HasPrefix(want, "THREW:") {
		n[0]++
	} else {
		n[1]++
	}
	if got != want {
		t.Fatalf("%s(%s):\ngot  %s\nwant %s", op, args, got, want)
	}
}

const liveDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func randomDigest(r *rand.Rand) string {
	switch r.IntN(24) {
	case 0:
		return "sha256:" + strings.Repeat("A", 64)
	case 1:
		return "sha256:zz"
	case 2:
		return "mapping"
	case 3:
		return ""
	}
	return "sha256:" + strings.Repeat(string("0123456789abcdef"[r.IntN(16)]), 64)
}

func randomImage(r *rand.Rand) jsjson.Value {
	repo := []string{"ghcr.io/x/y", "node", "a:5000/b", "a/b:1", "-bad", "x.y_z/w"}[r.IntN(6)]
	digest := strings.Repeat(string("0123456789abcdef"[r.IntN(16)]), 64)
	switch r.IntN(24) {
	case 0:
		return jsjson.MakeNumber(1)
	case 1:
		return jsjson.MakeNull()
	case 2:
		return jsjson.MakeString("")
	case 3:
		return jsjson.MakeArray(nil)
	case 4:
		return jsjson.MakeString(repo + "@sha256:" + strings.ToUpper(digest))
	case 5:
		return jsjson.MakeString(repo)
	}
	return jsjson.MakeString(repo + "@sha256:" + digest)
}

func randomMappingDoc(r *rand.Rand) string {
	body := jsjson.NewObj()
	for n := r.IntN(5); n >= 0; n-- {
		switch r.IntN(8) {
		case 0:
			body.Set([]string{"schemaVersion", "generatedAt", "source", "provenanceDigest"}[r.IntN(4)], randomImage(r))
		default:
			body.Set(randomDigest(r), randomImage(r))
		}
	}
	root := body
	if r.IntN(2) == 0 {
		root = jsjson.NewObj()
		root.Set("schemaVersion", jsjson.MakeString("faultline.runtime-mapping.v1"))
		root.Set("mapping", jsjson.MakeObject(body))
		root.Set("provenanceDigest", jsjson.MakeString(liveDigest))
	}
	text := jsjson.Stringify(jsjson.MakeObject(root))
	switch r.IntN(10) {
	case 0:
		return gen.CorruptDeep(r, text)
	case 1:
		return []string{"[]", "null", `"s"`, "3", `{"mapping":[]}`, `{"mapping":"x"}`, `{"mapping":null,"` + liveDigest + `":"n@` + liveDigest + `"}`}[r.IntN(7)]
	}
	return text
}

func TestLiveRuntimeMapping(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(10, 1))
	for i := 0; i < 2000; i++ {
		text := randomMappingDoc(r)
		got := func() string {
			doc, err := jsjson.Parse(text)
			if err != nil {
				return "THREW:" + err.Error()
			}
			entries, err := gitinv.LoadRuntimeMapping(doc)
			if err != nil {
				return "THREW:" + jsexc.Message(err)
			}
			pairs := []jsjson.Value{}
			for _, e := range entries {
				pairs = append(pairs, jsjson.MakeArray([]jsjson.Value{jsjson.MakeString(e.Fingerprint), jsjson.MakeString(e.Image)}))
			}
			return canonicalOf(jsjson.MakeArray(pairs))
		}()
		liveCheck(t, c, "loadRuntimeMappingDocument", `{"text":`+jsjson.Quote(text)+"}", got)
	}
	for i := 0; i < 2000; i++ {
		var entries []gitinv.MappingEntry
		mapping := jsjson.NewObj()
		for n := r.IntN(4); n > 0; n-- {
			digest := "sha256:" + strings.Repeat(string("ab"[r.IntN(2)]), 64)
			if mapping.Field(digest).Kind() != jsjson.Undefined {
				continue
			}
			image := randomImage(r)
			if image.Kind() != jsjson.String {
				image = jsjson.MakeString("")
			}
			mapping.Set(digest, image)
			entries = append(entries, gitinv.MappingEntry{Fingerprint: digest, Image: image.Str()})
		}
		digest := "sha256:" + strings.Repeat(string("ab"[r.IntN(2)]), 64)
		homogeneity := []string{gitinv.Homogeneous, gitinv.Heterogeneous, gitinv.Empty}[r.IntN(3)]
		args := `{"fingerprintDigest":` + jsjson.Quote(digest) + `,"homogeneity":"` + homogeneity + `","mapping":` + jsjson.Stringify(jsjson.MakeObject(mapping))
		defaultImage := ""
		if r.IntN(3) > 0 {
			if v := randomImage(r); v.Kind() == jsjson.String {
				defaultImage = v.Str()
				args += `,"defaultImage":` + jsjson.Quote(defaultImage)
			}
		}
		image, err := gitinv.ResolveRuntimeImageForFingerprint(digest, homogeneity, entries, defaultImage)
		got := ""
		if err != nil {
			got = "THREW:" + jsexc.Message(err)
		} else {
			got = image
		}
		liveCheck(t, c, "resolveRuntimeImageForFingerprint", args+"}", got)
	}
}

func TestLiveFingerprint(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(10, 2))
	for i := 0; i < 300; i++ {
		dir := t.TempDir()
		for _, name := range gitinv.EnvironmentDescriptorFiles {
			if r.IntN(3) != 0 {
				continue
			}
			path := filepath.Join(dir, name)
			switch r.IntN(30) {
			case 0:
				os.Symlink("elsewhere", path) // broken
			case 1:
				os.WriteFile(filepath.Join(dir, "target"), []byte("t"), 0o644)
				os.Symlink("target", path)
			case 2:
				os.Mkdir(path, 0o755)
			case 3:
				_ = mkfifo(path)
			case 4:
				if os.Getuid() != 0 {
					os.WriteFile(path, []byte("x"), 0o000)
				}
			default:
				body := make([]byte, r.IntN(40))
				for j := range body {
					body[j] = byte(r.IntN(256))
				}
				os.WriteFile(path, body, 0o644)
			}
		}
		fp, err := gitinv.ComputeFingerprint(dir)
		liveCheck(t, c, "computeEnvironmentFingerprint", `{"dir":`+jsjson.Quote(dir)+"}", threwOr(fp, err))
		for _, name := range gitinv.EnvironmentDescriptorFiles {
			os.Chmod(filepath.Join(dir, name), 0o644)
		}
	}
	// A directory that does not exist yields the empty fingerprint on both sides.
	dir := filepath.Join(t.TempDir(), "missing")
	fp, err := gitinv.ComputeFingerprint(dir)
	liveCheck(t, c, "computeEnvironmentFingerprint", `{"dir":`+jsjson.Quote(dir)+"}", threwOr(fp, err))
}

func TestLiveConfig(t *testing.T) {
	c := oracle.Start(t)
	defer c.Close()
	r := rand.New(rand.NewPCG(10, 3))
	base := `{"schemaVersion":"faultline.config.v1","image":"ghcr.io/x/y@` + liveDigest + `","runtime":{"alias":"node","image":"ghcr.io/x/y@` + liveDigest + `"},"snapshot":{"trackedFilesOnly":false},"stores":{"incidents":"i","witnesses":"w","recordings":"r"}}`
	images := []string{"ghcr.io/x/y:1@" + liveDigest, "a:5000/b@" + liveDigest, "a:5000/b:1@" + liveDigest, "x", "", "ghcr.io/x/y@sha256:" + strings.Repeat("g", 64)}
	for i := 0; i < 400; i++ {
		repo := t.TempDir()
		cfg := filepath.Join(repo, ".faultline", "config.json")
		os.Mkdir(filepath.Join(repo, ".faultline"), 0o755)
		text := base
		switch r.IntN(6) {
		case 0:
			text = gen.CorruptDeep(r, text)
		}
		if v, _ := jsjson.Parse(text); v.Kind() == jsjson.Object && r.IntN(3) == 0 {
			img := jsjson.MakeString(images[r.IntN(len(images))])
			if r.IntN(2) == 0 {
				v.Obj().Set("image", img)
			} else if rt := v.Obj().Field("runtime"); rt.Kind() == jsjson.Object {
				rt.Obj().Set("image", img)
			}
			text = jsjson.Stringify(v)
		}
		switch r.IntN(10) {
		case 0: // missing
		case 1:
			os.Symlink("nowhere", cfg)
		case 2:
			os.Mkdir(cfg, 0o755)
		case 3:
			os.WriteFile(cfg, []byte(text[:len(text)/2]), 0o644)
		default:
			os.WriteFile(cfg, []byte(text), 0o644)
		}
		got := ""
		config, ok, err := gitinv.ReadConfig(repo)
		switch {
		case err != nil:
			got = "THREW:" + jsexc.Message(err)
		case !ok:
			got = "NULL"
		default:
			got = canonicalOf(config)
		}
		liveCheck(t, c, "readFaultLineConfig", `{"dir":`+jsjson.Quote(repo)+"}", got)
	}
}
