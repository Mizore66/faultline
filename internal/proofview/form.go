package proofview

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
)

// formBodyLimit is the review form's 32 KiB body limit.
const formBodyLimit = 32 * 1024

var errFormTooLarge = errors.New("review form is too large")

type formKey struct{}

// formBody is one request's captured body. data is final once done is
// closed.
type formBody struct {
	data    []byte
	over    bool
	aborted bool
	done    chan struct{}
}

func (f *formBody) add(b []byte) {
	if f.over {
		return
	}
	if len(f.data)+len(b) > formBodyLimit {
		f.over, f.data = true, nil
		return
	}
	f.data = append(f.data, b...)
}

// startForm queues the body of the head just handed to net/http. Called
// with c.mu held.
func (c *nodeConn) startForm() {
	if !c.s.opts.captureFormBody {
		return
	}
	c.curForm = &formBody{done: make(chan struct{})}
	c.forms = append(c.forms, c.curForm)
}

// finishForm completes the body being filled; aborted means the message
// did not end. Called with c.mu held.
func (c *nodeConn) finishForm(aborted bool) {
	if c.curForm == nil {
		return
	}
	c.curForm.aborted = aborted
	close(c.curForm.done)
	c.curForm = nil
}

// takeForm hands the next request its body.
func (c *nodeConn) takeForm() *formBody {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.forms) == 0 {
		return nil
	}
	f := c.forms[0]
	c.forms = c.forms[1:]
	return f
}

// nodeFormBody waits for the request body of a server started with
// captureFormBody and returns its bytes. A body over formBodyLimit yields
// errFormTooLarge.
func nodeFormBody(r *http.Request) ([]byte, error) {
	f, _ := r.Context().Value(formKey{}).(*formBody)
	if f == nil {
		return nil, errors.New("form body not captured")
	}
	<-f.done
	switch {
	case f.over:
		return nil, errFormTooLarge
	case f.aborted:
		return nil, errors.New("aborted")
	}
	return f.data, nil
}

var absoluteTarget = regexp.MustCompile(`(?i)^https?://`)

// nodeRequestPath is the request's pathname under the server's configured
// path processing.
func nodeRequestPath(r *http.Request) string {
	c, _ := r.Context().Value(connKey{}).(*nodeConn)
	if c == nil || !c.s.opts.reviewPaths {
		return requestPathname(r.RequestURI)
	}
	return reviewPathname(r.RequestURI)
}

// reviewPathname is the review server's requestPath: the query is dropped,
// an absolute http(s) target contributes its URL pathname, and repeated
// slashes collapse. Backslashes and dot segments are left alone, except in
// the URL pathname of an absolute target.
func reviewPathname(target string) string {
	raw, _, _ := strings.Cut(target, "?")
	if raw == "" {
		raw = "/"
	}
	if loc := absoluteTarget.FindStringIndex(raw); loc != nil {
		rest := raw[loc[1]:]
		if i := strings.IndexAny(rest, `/\#`); i < 0 || rest[i] == '#' {
			raw = "/"
		} else {
			path, _, _ := strings.Cut(strings.ReplaceAll(rest[i:], `\`, "/"), "#")
			raw = removeDotSegments(path)
		}
	}
	for strings.Contains(raw, "//") {
		raw = strings.ReplaceAll(raw, "//", "/")
	}
	if raw == "" {
		return "/"
	}
	return raw
}
