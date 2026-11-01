//go:build linux

package main

func readPlatformMachine(previous cpuCounters, status *MachineStatus) cpuCounters {
	return readLinuxMachine("/proc", previous, status)
}
