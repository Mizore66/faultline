package proofview

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Mizore66/faultline/internal/bundle"
	"github.com/Mizore66/faultline/internal/bundle/turnproof"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/turninv"
)

const (
	hubHTMLType = "text/html; charset=utf-8"
	hubTextType = "text/plain; charset=utf-8"
)

type hubReplyFunc func(w http.ResponseWriter, r *http.Request, status int, contentType, body string)

// UIHubServer serves an immutable snapshot of the artifacts discovered at
// startup.
type UIHubServer struct {
	server     *Server
	repository string
	artifacts  []DiscoveredFaultLineArtifact
}

// URL is the server's http://127.0.0.1:<port> address.
func (s *UIHubServer) URL() string { return s.server.URL() }

// Close closes the transport.
func (s *UIHubServer) Close() error { return s.server.Close() }

// Artifacts returns a copy of the artifacts captured at startup.
func (s *UIHubServer) Artifacts() []DiscoveredFaultLineArtifact {
	return append([]DiscoveredFaultLineArtifact{}, s.artifacts...)
}

// StartUIHubServer discovers the repository's artifacts once and serves them.
func StartUIHubServer(repository string, port float64) (*UIHubServer, error) {
	hub := &UIHubServer{repository: repository, artifacts: DiscoverFaultLineArtifacts(repository)}
	server, err := startNodeServer(port, func(string) http.HandlerFunc {
		reply := func(w http.ResponseWriter, r *http.Request, status int, contentType, body string) {
			h := w.Header()
			h.Set("Content-Type", contentType)
			if status == http.StatusOK {
				// Frozen TS sends no-store only on 200 responses.
				h.Set("Cache-Control", "no-store")
			}
			nodeHeaders(h, r)
			writeNodeBody(w, r, status, jsstr.ToUTF8(body))
		}
		return func(w http.ResponseWriter, r *http.Request) { hub.route(w, r, reply) }
	})
	if err != nil {
		return nil, err
	}
	hub.server = server
	return hub, nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (s *UIHubServer) route(w http.ResponseWriter, r *http.Request, reply hubReplyFunc) {
	path := requestPathname(r.RequestURI)
	if r.Method != "GET" {
		reply(w, r, http.StatusNotFound, hubTextType, "Not found")
		return
	}
	if path == "/" {
		reply(w, r, http.StatusOK, hubHTMLType, RenderHubIndex(s.repository, s.artifacts))
		return
	}
	var rest string
	raw := false
	if v, ok := strings.CutPrefix(path, "/artifact/"); ok {
		rest = v
	} else if v, ok := strings.CutPrefix(path, "/raw/"); ok {
		rest, raw = v, true
	}
	if !allDigits(rest) {
		reply(w, r, http.StatusNotFound, hubTextType, "Not found")
		return
	}
	index, err := strconv.Atoi(rest)
	if err != nil || index >= len(s.artifacts) {
		reply(w, r, http.StatusNotFound, hubTextType, "Artifact not found")
		return
	}
	artifact := s.artifacts[index]
	isGit, isTurn := artifact.Kind == "git-proof-bundle", artifact.Kind == "turn-proof-bundle"
	if raw && isGit || !raw && !isGit && !isTurn {
		reply(w, r, http.StatusNotFound, hubTextType, "Artifact not found")
		return
	}
	switch {
	case isGit:
		view, err := LoadVerifiedGitProofView(artifact.Path, nil)
		if err != nil {
			reply(w, r, http.StatusInternalServerError, hubTextType, err.Error())
			return
		}
		reply(w, r, http.StatusOK, hubHTMLType, RenderGitProofIncidentPage(view))
	case isTurn:
		serveHubTurn(w, r, artifact, raw, reply)
	default:
		page, err := RenderRawJSONPage(artifact.Path)
		if err != nil {
			reply(w, r, http.StatusInternalServerError, hubTextType, err.Error())
			return
		}
		reply(w, r, http.StatusOK, hubHTMLType, page)
	}
}

func serveHubTurn(w http.ResponseWriter, r *http.Request, artifact DiscoveredFaultLineArtifact, raw bool, reply hubReplyFunc) {
	verified := turnproof.Verify(artifact.Path, "", false)
	if !verified.Valid {
		reply(w, r, http.StatusOK, hubHTMLType, RenderTurnVerificationFailure(verified.Errors))
		return
	}
	file := nodefs.Join(artifact.Path, "investigation.json")
	if raw {
		page, err := RenderRawJSONPage(file)
		if err != nil {
			reply(w, r, http.StatusInternalServerError, hubTextType, err.Error())
			return
		}
		reply(w, r, http.StatusOK, hubHTMLType, page)
		return
	}
	value, err := bundle.ParseJSONFile(file)
	if err == nil {
		value, err = bundle.ParseValue(value, turninv.ResultSchema)
	}
	if err != nil {
		reply(w, r, http.StatusInternalServerError, hubTextType, err.Error())
		return
	}
	reply(w, r, http.StatusOK, hubHTMLType, RenderTurnDashboard(value))
}
