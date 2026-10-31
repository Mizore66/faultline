package proofview

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
)

// JudgePreview is writeJudgePreview's result.
type JudgePreview struct {
	Path  string
	Bytes int
}

// Port of src/judge-preview.ts defaultJudgePreviewPath.
func DefaultJudgePreviewPath() string {
	return nodefs.Resolve("docs", "judge-preview.html")
}

// Port of src/judge-preview.ts writeJudgePreview and safePreviewOutput.
// Existing path components are lstat-checked and never followed. A dangling
// symlink output is refused: this is the approved Go policy; the raw pinned TS
// follows it. This is not race-free confinement.
func WriteJudgePreview(outputFile string) (result JudgePreview, err error) {
	err = jsexc.Try(func() { result = writeJudgePreview(outputFile) })
	return result, err
}

func writeJudgePreview(outputFile string) JudgePreview {
	output := nodefs.Resolve(outputFile)
	base := nodefs.Relative(nodefs.Dirname(output), output)
	// path.extname: from the last dot, which must not be the first byte.
	dot := strings.LastIndexByte(base, '.')
	if dot <= 0 || !strings.EqualFold(base[dot:], ".html") {
		jsexc.Throw(errors.New("Judge preview output must be an .html file."))
	}
	safedir.EnsureRealDirectoryTree(nodefs.Dirname(output),
		"Judge preview output must use a real directory: ",
		"Judge preview output cannot traverse a symbolic link or non-directory: ")
	stat, err := nodefs.Lstat(output)
	if err != nil {
		var ne *nodefs.Error
		if !errors.As(err, &ne) || ne.Code != "ENOENT" {
			jsexc.Throw(err)
		}
	} else if stat.Mode()&fs.ModeSymlink != 0 || !stat.Mode().IsRegular() {
		jsexc.Throw(errors.New(jsexc.Concat("Judge preview output must be a regular non-symlink file: ", output)))
	}
	page := jsexc.Must(RenderJudgePreviewPage(nil))
	jsexc.Must(struct{}{}, nodefs.WriteFileUTF8(output, page, 0o644, false))
	return JudgePreview{Path: output, Bytes: len(jsstr.ToUTF8(page))}
}
