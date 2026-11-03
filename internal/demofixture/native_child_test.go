package demofixture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Mizore66/faultline/internal/canonical"
	"github.com/Mizore66/faultline/internal/clock"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nativefixture"
)

var nativeLiveOut = flag.String("native-live-out", "", "test-only exclusive live-reference evidence directory")

func TestMain(m *testing.M) {
	if code, handled := nativefixture.DispatchChild(os.Args[1:], os.Getenv(nativefixture.ChildEnvName), os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func nativeExpectedProcess(state jsjson.Value) (config, stdout, stderr string, status int) {
	source, renderer, partial := false, false, false
	for _, h := range state.Get("hunks").Items() {
		source = source || h.Str() == "settlement-display-default"
		renderer = renderer || h.Str() == "settlement-display-boundary"
		partial = partial || h.Str() == "partial-renderer-hunk"
	}
	display, settlement := "ORDER_CURRENCY", "ORDER_CURRENCY"
	if source {
		display = "EUR"
	}
	if renderer {
		settlement = "DISPLAY_CURRENCY"
	}
	config = fmt.Sprintf("{\"displayDefault\":%q,\"settlementSource\":%q}\n", display, settlement)
	if partial {
		return "{\"displayDefault\":\"ORDER_CURRENCY\",\"settlementSource\":}\n", "", "native fixture config: missing value after object key\n", 3
	}
	if source && renderer {
		return config, "{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_FAIL\"}\n", "expected settlement USD; received EUR\n", 1
	}
	return config, "settlement currency preserved\n{\"protocol\":\"faultline.witness-result.v1\",\"outcome\":\"PREDICATE_PASS\"}\n", "", 0
}

func TestNativeAnalysisRealStates(t *testing.T) {
	e, err := nativefixture.NewExecutor()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range fixtureStates() {
		t.Run(state.Get("id").Str(), func(t *testing.T) {
			config, stdout, stderr, status := nativeExpectedProcess(state)
			materialized, err := nativefixture.Materialize(nativeContractStrings(state.Get("hunks")))
			if err != nil || string(materialized.Data) != config {
				t.Fatalf("materialization differs: %q %v", materialized.Data, err)
			}
			c, err := e.Run(context.Background(), materialized)
			if err != nil || c.Status == nil || *c.Status != status || string(c.Stdout) != stdout || string(c.Stderr) != stderr || c.Incomplete || c.CleanupError != "" || c.FailureKind != "" {
				t.Fatalf("actual child differs: status=%v stdout=%q stderr=%q error=%v", c.Status, c.Stdout, c.Stderr, err)
			}
			if _, err := os.Stat(c.Cwd); !os.IsNotExist(err) {
				t.Fatalf("owned directory not removed: %v", err)
			}
		})
	}
}

func nativeLiveSequence() []struct {
	state   jsjson.Value
	attempt int
} {
	states := fixtureStates()
	var out []struct {
		state   jsjson.Value
		attempt int
	}
	add := func(index, attempt int) {
		out = append(out, struct {
			state   jsjson.Value
			attempt int
		}{states[index], attempt})
	}
	for i := idxContributionAtlas; i <= idxContributionDelta; i++ {
		add(i, 0)
	}
	for i := idxTurn1; i <= idxTurn8; i++ {
		add(i, 0)
	}
	for _, index := range []int{idxTurn4, idxTurn5, idxTurn6, idxTurn7, idxTurn7, idxTurn8} {
		for attempt := 1; attempt <= 3; attempt++ {
			add(index, attempt)
		}
	}
	for _, index := range []int{idxSourceOnly, idxRendererOnly, idxUnresolved, idxCandidate, idxReverted, idxLastGood, idxLastGood, idxFirstBad, idxRepaired} {
		add(index, 0)
	}
	return out
}

func nativeLiveAudit(t *testing.T, result NativeResult) []jsjson.Value {
	t.Helper()
	p, a := result.Provenance, result.Analysis
	if p.Get("status").Str() != "COMPLETE" || a.Get("grade", "value").Str() != "A" || !a.Get("prevention", "verified").Bool() {
		t.Fatal("live analysis is not complete and proved")
	}
	file, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if p.Get("environmentPreimage", "executableSHA256").Str() != fmt.Sprintf("sha256:%x", sha256.Sum256(binary)) || p.Get("environmentPreimage", "runtime").Str() != runtime.Version() || p.Get("environmentPreimage", "goos").Str() != runtime.GOOS || p.Get("environmentPreimage", "goarch").Str() != runtime.GOARCH {
		t.Fatal("actual executable/runtime identity differs")
	}
	envDigest, err := canonical.DigestJSON(p.Get("environmentPreimage"))
	if err != nil || envDigest != p.Get("environmentDigest").Str() {
		t.Fatal("environment digest differs", err)
	}
	env := p.Get("environmentPreimage", "environment")
	wantEnv := []string{nativefixture.ChildEnvName + "=" + nativefixture.ChildEnvValue}
	if runtime.GOOS == "windows" {
		wantEnv = append(wantEnv, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
	}
	requireNativeValue(t, "actual minimal environment", env, strArray(wantEnv...))
	if len(p.Get("nonce").Str()) != 36 {
		t.Fatal("missing fresh nonce")
	}
	sequence, receipts, catalog := nativeLiveSequence(), p.Get("invocations").Items(), a.Get("runCatalog").Items()
	if len(sequence) != 39 || len(receipts) != 39 || len(catalog) != 39 {
		t.Fatal("live coverage is not39")
	}
	byID := map[string]jsjson.Value{}
	for _, record := range catalog {
		if _, exists := byID[record.Get("id").Str()]; exists {
			t.Fatal("duplicate catalog identity")
		}
		byID[record.Get("id").Str()] = record
	}
	cwdSet := map[string]bool{}
	var captures []jsjson.Value
	for i, step := range sequence {
		r := receipts[i]
		config, stdout, stderr, status := nativeExpectedProcess(step.state)
		if int(r.Get("ordinal").Num()) != i+1 || r.Get("stateId").Str() != step.state.Get("id").Str() || int(r.Get("executionAttempt").Num()) != step.attempt || !r.Get("accepted").Bool() || int(r.Get("status").Num()) != status || r.Get("signal").Kind() != jsjson.Null || r.Get("failureKind").Kind() != jsjson.Null || r.Get("cleanupError").Kind() != jsjson.Null {
			t.Fatalf("receipt%d identity/status/failure differs", i+1)
		}
		if !bytes.Equal(nativeContractBlob(t, r.Get("config")), []byte(config)) || !bytes.Equal(nativeContractBlob(t, r.Get("stdout")), []byte(stdout)) || !bytes.Equal(nativeContractBlob(t, r.Get("stderr")), []byte(stderr)) {
			t.Fatalf("receipt%d original bytes differ", i+1)
		}
		if r.Get("expectedPartial").Bool() != (status == 3) || r.Get("executable").Str() != file {
			t.Fatal("configuration flag or actual executable path differs")
		}
		requireNativeValue(t, "actual argv", r.Get("argv"), strArray(file, nativefixture.ChildArg))
		requireNativeValue(t, "receipt environment", r.Get("environment"), env)
		cwd := r.Get("cwd").Str()
		if !filepath.IsAbs(cwd) || cwdSet[cwd] {
			t.Fatal("owned cwd missing or reused")
		}
		cwdSet[cwd] = true
		if _, err := os.Stat(cwd); !os.IsNotExist(err) {
			t.Fatalf("owned cwd remains: %v", err)
		}
		for _, key := range []string{"startedAt", "executedAt"} {
			v := nativeContractTime(t, r.Get(key).Str())
			if v.IsZero() || clock.ISO(v) != r.Get(key).Str() {
				t.Fatal("missing observed wall timestamp")
			}
		}
		if r.Get("durationMs").Num() < 0 {
			t.Fatal("negative evidence duration")
		}
		preimage := r.Get("runIdPreimage")
		id, err := canonical.DigestJSON(preimage)
		if err != nil || id != r.Get("runId").Str() {
			t.Fatal("complete canonical run-ID preimage differs", err)
		}
		if preimage.Get("nonce").Str() != p.Get("nonce").Str() || int(preimage.Get("ordinal").Num()) != i+1 || int(preimage.Get("executionAttempt").Num()) != step.attempt || preimage.Get("configDigest").Str() != r.Get("config", "sha256").Str() || preimage.Get("startedAt").Str() != r.Get("startedAt").Str() || preimage.Get("status").Num() != r.Get("status").Num() {
			t.Fatal("preimage observation binding differs")
		}
		record, exists := byID[id]
		if !exists {
			t.Fatal("receipt is absent from catalog")
		}
		delete(byID, id)
		requireNativeValue(t, "catalog receipt record", record, r.Get("runRecord"))
		body := jsjson.NewObj()
		for _, key := range record.Obj().Keys() {
			if key != "id" {
				body.Set(key, record.Get(key))
			}
		}
		requireNativeValue(t, "complete preimage record body", preimage.Get("record"), jsjson.MakeObject(body))
		if record.Get("witnessDigest").Str() != a.Get("witness", "digest").Str() || record.Get("environmentDigest").Str() != envDigest || record.Get("stdout").Str() != stdout || record.Get("stderr").Str() != stderr || record.Get("executedAt").Str() != r.Get("executedAt").Str() || record.Get("durationMs").Num() != r.Get("durationMs").Num() || record.Get("executionKind").Str() != "EXECUTED" {
			t.Fatal("record is not bound to actual receipt")
		}
		c := jsjson.NewObj()
		c.Set("state", step.state)
		c.Set("executionAttempt", r.Get("executionAttempt"))
		for _, key := range []string{"config", "expectedPartial", "executable", "argv", "cwd", "environment", "startedAt", "executedAt", "durationMs", "status", "signal", "stdout", "stderr"} {
			c.Set(key, r.Get(key))
		}
		c.Set("incomplete", jsjson.MakeBool(false))
		c.Set("failureKind", jsjson.MakeNull())
		c.Set("cleanupError", jsjson.MakeNull())
		captures = append(captures, jsjson.MakeObject(c))
	}
	if len(byID) != 0 {
		t.Fatal("catalog has executions without receipts")
	}
	return captures
}

func TestNativeAnalysisRealExecution(t *testing.T) {
	// Explicit authorization of this reviewed built-in model, recorded now for
	// this test invocation. No historical approval or human authentication claim.
	approval := NativeApproval{ProposalDigest: NativeWitnessProposal().Get("digest").Str(), ApprovedAt: clock.ISO(clock.Now())}
	result, err := RunNativeAnalysis(context.Background(), approval)
	if err != nil {
		t.Fatal(err)
	}
	nativeLiveAudit(t, result)
}

func TestNativeAnalysisLiveReferenceCapture(t *testing.T) {
	approval := NativeApproval{ProposalDigest: NativeWitnessProposal().Get("digest").Str(), ApprovedAt: clock.ISO(clock.Now())}
	var entry time.Time
	reads := 0
	result, err := runNativeAnalysis(context.Background(), approval, nativeDeps{
		newExecutor: func() (nativeExecutor, error) { return nativefixture.NewExecutor() }, nonce: clock.NewUUID,
		now: func() time.Time {
			v := clock.Now()
			reads++
			if reads == 1 {
				entry = v
			}
			return v
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	captures := nativeLiveAudit(t, result)
	if reads != 2 {
		t.Fatal("live API clock is not exactly entry/final")
	}
	if *nativeLiveOut == "" {
		return
	}
	input := jsjson.MustObject("inputKind", jsjson.MakeString("observed-native-process-transcript"), "proposal", result.Provenance.Get("proposal"), "approval", result.Provenance.Get("approval"), "environmentPreimage", result.Provenance.Get("environmentPreimage"), "nonce", result.Provenance.Get("nonce"), "startedAt", jsjson.MakeString(clock.ISO(entry)), "generatedAt", result.Provenance.Get("generatedAt"), "captures", jsjson.MakeArray(captures))
	actual := jsjson.StringifyIndent(jsjson.MustObject("analysis", result.Analysis, "provenance", result.Provenance), "  ") + "\n"
	retained := jsjson.MustObject("input", input, "actual", jsjson.MakeString(actual))
	for name, text := range map[string]string{"observed-input.json": jsjson.StringifyIndent(retained, "  ") + "\n", "actual-result.json": actual} {
		f, err := os.OpenFile(filepath.Join(*nativeLiveOut, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := f.Write([]byte(jsstr.ToUTF8(text)))
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatalf("evidence write=%v close=%v", writeErr, closeErr)
		}
	}
}

func TestNativeAnalysisResultIsolation(t *testing.T) {
	tc := nativeContractCases(t)[0]
	result, err := runNativeAnalysis(context.Background(), tc.Approval, tc.Deps)
	if err != nil {
		t.Fatal(err)
	}
	before := jsjson.Stringify(result.Provenance)
	result.Analysis.Get("witness", "policy").Obj().Set("note", jsjson.MakeString("caller change"))
	result.Analysis.Get("runCatalog").Items()[0].Obj().Set("stdout", jsjson.MakeString("caller change"))
	if jsjson.Stringify(result.Provenance) != before {
		t.Fatal("analysis mutation changed execution provenance")
	}
	if strings.Contains(before, "caller change") {
		t.Fatal("invalid isolation baseline")
	}
}
