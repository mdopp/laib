package stick

import (
	"fmt"
	"strconv"
	"strings"
)

// diskNumber turns "2", "PhysicalDrive2" or "\\.\PhysicalDrive2" (Windows
// spellings) into the disk number.
func diskNumber(device string) (int, error) {
	s := strings.ToLower(strings.TrimSpace(device))
	s = strings.TrimPrefix(s, `\\.\`)
	s = strings.TrimPrefix(s, "physicaldrive")
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q is not a disk number (e.g. 2 or \\\\.\\PhysicalDrive2)", device)
	}
	return n, nil
}
