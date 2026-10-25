package runtimeimage

import (
	"time"

	"github.com/Mizore66/faultline/internal/hostcmd"
	"github.com/Mizore66/faultline/internal/jsexc"
)

// Runner runs `docker <args>` and reports what the TS runners'
// DockerImageInspectResult holds. The callers in this package pass the
// command's timeout and the output cap createNodeDockerImageRunner picks
// for args (maxOutputBytesFor). A runner whose TS promise would reject
// throws with jsexc.Throw; the callers turn that into their
// "FaultLine could not ..." errors.
type Runner func(args []string, timeout time.Duration, maxOutputBytes int) hostcmd.Result

// maxOutputBytesFor ports the cap selection of src/runtime.ts:917
// createNodeDockerImageRunner: by command.arguments[0].
func maxOutputBytesFor(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "pull":
			return DockerImagePullMaxOutputBytes
		case "build":
			return DockerProjectImageBuildMaxOutputBytes
		}
	}
	return DockerImageInspectMaxOutputBytes
}

// NodeRunner ports src/runtime.ts:917 createNodeDockerImageRunner (and the
// createNodeDockerImage{Inspect,Pull}Runner and
// createNodeDockerProjectImageBuildRunner wrappers at 989-1005): `docker`
// spawned without a shell through hostcmd, an 'error' event's message
// joined to stderr with "\n".
func NodeRunner() Runner {
	return func(args []string, timeout time.Duration, maxOutputBytes int) hostcmd.Result {
		r, err := hostcmd.Run("docker", args, hostcmd.Options{Timeout: timeout, MaxOutputBytes: maxOutputBytes, ErrorJoin: "\n"})
		if err != nil {
			jsexc.Throw(err) // never: the TS promise does not reject
		}
		return r
	}
}
