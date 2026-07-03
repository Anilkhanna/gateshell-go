package collector

const bytesPerGB = 1024.0 * 1024.0 * 1024.0

// calcDiskUsage converts raw statfs(2) counters (block size in bytes,
// total blocks, blocks available to unprivileged users) into used/total
// GB. "used" is total minus space available to the calling user, not
// minus raw free blocks -- the latter would count root-reserved space as
// available, understating what a normal user actually sees as used. This
// mirrors the convention used by `df`/psutil.
func calcDiskUsage(blockSizeBytes, totalBlocks, availBlocks uint64) (usedGB, totalGB float64) {
	total := float64(totalBlocks) * float64(blockSizeBytes)
	avail := float64(availBlocks) * float64(blockSizeBytes)
	used := total - avail
	if used < 0 {
		used = 0
	}
	return used / bytesPerGB, total / bytesPerGB
}
