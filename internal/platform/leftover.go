package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// A llama-server that outlives the app (crash, kill -9) keeps the model in
// memory and holds the port, so the next start cannot start its own. Windows
// (job object) and Linux (parent-death signal) stop it with the app; macOS has
// neither, so the app records the server it started and the next start stops
// it (proc_darwin.go). The decisions are here, without a build tag, so they are
// tested on every OS.

// childRecord is what the app writes about the llama-server it started.
type childRecord struct {
	PID  int    `json:"pid"`
	Path string `json:"path"` // the executable the app started
}

// childRecordPath is the record file for the server on port. It includes the
// port so copies of the app configured for different ports, which share the
// macOS data folder, do not overwrite each other's record.
func childRecordPath(dir string, port int) string {
	return filepath.Join(dir, fmt.Sprintf("llama-server-%d.pid", port))
}

func encodeChildRecord(r childRecord) ([]byte, error) { return json.Marshal(r) }

func parseChildRecord(data []byte) (childRecord, error) {
	var r childRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return childRecord{}, err
	}
	if r.PID <= 1 || !path.IsAbs(r.Path) {
		return childRecord{}, errors.New("invalid server process record")
	}
	return r, nil
}

// isLeftoverServer reports whether the process described by psLine is the
// server in rec, left behind by an app that died. psLine is the output of
// `ps -ww -p <rec.PID> -o ppid=,comm=` ("<ppid> <executable path>"). It is
// deliberately strict, because a false yes kills someone else's process:
//   - the executable must be exactly the one the app started (a PID reused by
//     another program, including the user's own llama-server elsewhere, has a
//     different path);
//   - the parent must be launchd (pid 1): the server of a running app (for
//     example another copy of it) still has that app as its parent;
//   - it is never this process or pid 0/1.
//
// Anything unexpected (empty output, a bare name instead of a path) means no.
func isLeftoverServer(rec childRecord, psLine string, self int) bool {
	if rec.PID <= 1 || rec.PID == self || !path.IsAbs(rec.Path) {
		return false
	}
	s := strings.TrimSpace(psLine)
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return false
	}
	ppid, err := strconv.Atoi(s[:i])
	if err != nil || ppid != 1 {
		return false
	}
	comm := strings.TrimSpace(s[i:])
	return path.IsAbs(comm) && path.Clean(comm) == path.Clean(rec.Path)
}

// checkServerPort returns an error the user can act on when something already
// answers on the server's port, which is usually a llama-server left over from
// an earlier run.
func checkServerPort(port int) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 250*time.Millisecond)
	if err != nil {
		return nil
	}
	conn.Close()
	return errors.New(portInUseMessage(port, runtime.GOOS))
}

func portInUseMessage(port int, goos string) string {
	var how string
	switch goos {
	case "windows":
		how = "End llama-server.exe in Task Manager (Details tab), then start the app again."
	case "darwin":
		how = fmt.Sprintf("Quit llama-server in Activity Monitor (or find it with: lsof -i :%d), then start the app again.", port)
	default:
		how = fmt.Sprintf("Find it with: ss -ltnp 'sport = :%d' (or lsof -i :%d), stop it, then start the app again.", port, port)
	}
	return fmt.Sprintf("Port %d is already in use, so the translation server cannot start.\n"+
		"A llama-server left over from an earlier run (for example after a crash) may still be holding it. %s\n"+
		"If another program uses this port, set a different \"port\" in config.json.", port, how)
}
