package utils

import "fmt"

// HumanBytes formats a byte count as a human-readable size using binary units
// (e.g. 6451 -> "6.3 KiB", 1024 -> "1.0 KiB", 512 -> "512 B").
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
