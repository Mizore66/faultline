package proofview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/nodefs"
)

// UIHubEmptyNextCommand is the next command shown when no artifacts exist.
const UIHubEmptyNextCommand = "fl quickstart"

// DiscoveredFaultLineArtifact is one artifact found under .faultline.
type DiscoveredFaultLineArtifact struct {
	Kind  string
	ID    string
	Path  string
	Label string
}

func isDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

func isFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func hubManifestIsTurn(dir string) bool {
	manifest := filepath.Join(dir, "manifest.json")
	if !isFile(manifest) {
		return false
	}
	data, err := nodefs.ReadBytes(manifest)
	if err != nil {
		return false
	}
	var parsed struct {
		SchemaVersion any `json:"schemaVersion"`
	}
	if json.Unmarshal(data, &parsed) != nil {
		return false
	}
	value, ok := parsed.SchemaVersion.(string)
	return ok && strings.Contains(value, "turn-proof")
}

func hasBundleFiles(dir string) bool {
	return isFile(filepath.Join(dir, "ROOT.sha256")) &&
		isFile(filepath.Join(dir, "manifest.json")) &&
		isFile(filepath.Join(dir, "investigation.json"))
}

func looksLikeGitProofBundle(dir string) bool {
	return hasBundleFiles(dir) && !hubManifestIsTurn(dir)
}

func looksLikeTurnProofBundle(dir string) bool {
	return hasBundleFiles(dir) && hubManifestIsTurn(dir)
}

// scanBundleRoots lists child directories of root that match the wanted kinds.
func scanBundleRoots(root string, git, turn bool) []DiscoveredFaultLineArtifact {
	out := []DiscoveredFaultLineArtifact{}
	if !isDirectory(root) {
		return out
	}
	entries, err := nodefs.ReadDirTypes(root)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir || strings.HasPrefix(entry.Name, ".") {
			continue
		}
		dir := filepath.Join(root, entry.Name)
		kind := ""
		if turn && looksLikeTurnProofBundle(dir) {
			kind = "turn-proof-bundle"
		} else if git && looksLikeGitProofBundle(dir) {
			kind = "git-proof-bundle"
		}
		if kind != "" {
			out = append(out, DiscoveredFaultLineArtifact{Kind: kind, ID: entry.Name, Path: dir, Label: entry.Name})
		}
	}
	return out
}

func scanJSONFiles(root, kind string, labelFilename bool, skipCache bool) []DiscoveredFaultLineArtifact {
	out := []DiscoveredFaultLineArtifact{}
	if !isDirectory(root) {
		return out
	}
	entries, err := nodefs.ReadDirTypes(root)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsFile || strings.HasPrefix(entry.Name, ".") || !strings.HasSuffix(entry.Name, ".json") {
			continue
		}
		if skipCache && strings.Contains(entry.Name, "turn-snapshot-cache") {
			continue
		}
		id := strings.TrimSuffix(entry.Name, ".json")
		label := id
		if labelFilename {
			label = entry.Name
		}
		out = append(out, DiscoveredFaultLineArtifact{Kind: kind, ID: id, Path: filepath.Join(root, entry.Name), Label: label})
	}
	return out
}

// DiscoverFaultLineArtifacts inspects only the fixed roots under
// <repository>/.faultline and never follows symlinks.
func DiscoverFaultLineArtifacts(repository string) []DiscoveredFaultLineArtifact {
	result := []DiscoveredFaultLineArtifact{}
	absolute, err := filepath.Abs(repository)
	if err != nil {
		return result
	}
	base := filepath.Join(absolute, ".faultline")
	if !isDirectory(base) {
		return result
	}
	var found []DiscoveredFaultLineArtifact
	found = append(found, scanBundleRoots(filepath.Join(base, "git-proof-bundles"), true, false)...)
	found = append(found, scanBundleRoots(filepath.Join(base, "turn-proof-bundles"), false, true)...)
	for _, name := range []string{"bundles", "proofs", "git-proofs", "turn-proofs"} {
		found = append(found, scanBundleRoots(filepath.Join(base, name), true, true)...)
	}
	found = append(found, scanJSONFiles(filepath.Join(base, "incidents", "drafts"), "incident-draft", false, false)...)
	found = append(found, scanJSONFiles(filepath.Join(base, "recordings"), "ledger", true, true)...)

	seen := map[string]bool{}
	for _, artifact := range found {
		key := filepath.Clean(artifact.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, artifact)
	}
	return result
}

const hubIndexStyle = `body{margin:0;padding:32px;background:#fff;color:#333;font-family:Circular,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}main{max-width:900px;margin:0 auto}h1{font-weight:400;letter-spacing:-.03em}ul{list-style:none;padding:0}li{padding:12px 0;border-top:1px solid #ddd}a{color:#333;text-decoration:none}small{display:block;color:#777;margin-top:4px;word-break:break-all}code{font-family:"SFMono-Regular",Consolas,monospace}.empty-state{border:1px dashed #deded8;border-radius:10px;padding:14px;color:#8a6030;background:#f5f5f2}`

func renderHubRow(index int, artifact DiscoveredFaultLineArtifact) string {
	href := "/raw/" + strconv.Itoa(index)
	if artifact.Kind == "git-proof-bundle" || artifact.Kind == "turn-proof-bundle" {
		href = "/artifact/" + strconv.Itoa(index)
	}
	return `<li><a href="` + href + `"><strong>` + EscapeHTML(artifact.Kind) + `</strong> — ` + EscapeHTML(artifact.Label) + `</a><small>` + EscapeHTML(artifact.Path) + `</small></li>`
}

// RenderHubIndex renders the hub index page for the discovered artifacts.
func RenderHubIndex(repository string, artifacts []DiscoveredFaultLineArtifact) string {
	var body strings.Builder
	if len(artifacts) == 0 {
		body.WriteString(`<p class="empty-state">No local artifacts found. Run <code>` + EscapeHTML(UIHubEmptyNextCommand) + `</code> to create one.</p>`)
	} else {
		body.WriteString("<ul>")
		for index, artifact := range artifacts {
			body.WriteString(renderHubRow(index, artifact))
		}
		body.WriteString("</ul>")
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>FaultLine UI hub</title><style>` + hubIndexStyle + `</style></head><body><main><h1>FaultLine UI hub</h1><p><code>` + EscapeHTML(repository) + `</code></p>` + body.String() + `</main></body></html>`
}
