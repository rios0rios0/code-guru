package support

import (
	"sync"
	"syscall"

	logger "github.com/sirupsen/logrus"
)

// prSetDumpable is PR_SET_DUMPABLE from <linux/prctl.h>.
const prSetDumpable = 4

//nolint:gochecknoglobals // guards a process-wide, idempotent state change
var hardenOnce sync.Once

// HardenAgainstChildInspection marks this process non-dumpable. Its
// `/proc/<pid>/environ`, `/proc/<pid>/mem` and friends then belong to root, so
// a child running as the same user — the agentic reviewer's CLI, and any
// command a prompt injection talks it into — cannot read the provider token
// out of this process's environment or memory, and cannot attach to it with
// ptrace. The process itself is unaffected, and the flag is not inherited:
// every exec'd child starts dumpable again. Idempotent; a failure is logged
// and the review proceeds, since the agent's own permission rules remain in
// force either way.
func HardenAgainstChildInspection() {
	hardenOnce.Do(func() {
		if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0); errno != 0 {
			logger.Warnf("could not mark the process non-dumpable before starting the agent: %v", errno)
		}
	})
}
