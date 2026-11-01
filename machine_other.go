//go:build !linux

package main

func readPlatformMachine(previous cpuCounters, status *MachineStatus) cpuCounters {
	status.Errors["cpu"] = "CPU sampling is currently supported on Linux"
	status.Errors["memory"] = "Memory sampling is currently supported on Linux"
	status.Errors["uptime"] = "Uptime sampling is currently supported on Linux"
	return cpuCounters{}
}
