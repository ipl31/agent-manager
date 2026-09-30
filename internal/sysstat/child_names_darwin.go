package sysstat

import "github.com/shirou/gopsutil/v4/process"

func lookupChildNames(wanted []childRef) []namedChild {
	named := make([]namedChild, 0, len(wanted))
	for _, child := range wanted {
		// NewProcess probes existence and creation time before callers ask for
		// fields. This handle avoids those redundant native queries.
		proc := &process.Process{Pid: int32(child.pid)}
		parent, err := proc.Ppid()
		if err != nil || int(parent) != child.parent {
			continue
		}
		argv, err := proc.CmdlineSlice()
		if err != nil || len(argv) == 0 || argv[0] == "" {
			continue
		}
		named = append(named, namedChild{
			pid:     child.pid,
			parent:  int(parent),
			command: argv[0],
		})
	}
	return named
}
