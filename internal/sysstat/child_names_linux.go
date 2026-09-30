package sysstat

import (
	"os/exec"
	"strconv"
	"strings"
)

func lookupChildNames(wanted []childRef) []namedChild {
	pids := make([]string, 0, len(wanted))
	for _, child := range wanted {
		pids = append(pids, strconv.Itoa(child.pid))
	}
	// ps exits non-zero when every pid it was given has gone, which is a
	// child that ended between the two calls rather than a failure: there is
	// nothing left to name and the next sample sees whatever replaced it.
	out, err := exec.Command("ps", "-o", "pid=,ppid=,args=", "-p", strings.Join(pids, ",")).Output()
	if err != nil {
		return nil
	}
	return parseChildNames(string(out))
}

func parseChildNames(psOutput string) []namedChild {
	var named []namedChild
	for _, line := range strings.Split(strings.TrimSpace(psOutput), "\n") {
		pidText, rest := nextField(line)
		ppidText, rest := nextField(rest)
		command, _ := nextField(rest)
		pid, err1 := strconv.Atoi(pidText)
		ppid, err2 := strconv.Atoi(ppidText)
		if err1 != nil || err2 != nil || command == "" {
			continue
		}
		named = append(named, namedChild{pid: pid, parent: ppid, command: command})
	}
	return named
}

func nextField(line string) (string, string) {
	line = strings.TrimLeft(line, " ")
	if i := strings.IndexByte(line, ' '); i >= 0 {
		return line[:i], line[i+1:]
	}
	return line, ""
}
