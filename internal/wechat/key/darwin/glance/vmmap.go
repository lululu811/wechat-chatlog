package glance

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/chenliitaz/chatlog/internal/errors"
)

const (
	FilterRegionType  = "MALLOC_NANO"
	FilterRegionType2 = "MALLOC_SMALL"
	FilterSHRMOD      = "SM=PRV"
	EmptyFlag         = "(empty)"
	CommandVmmap      = "vmmap"
	CommandUname      = "uname"
)

// FilterRegionTypes27 covers the renamed region families on Darwin 27
// (macOS 27). The old "MALLOC_NANO / MALLOC_SMALL / MALLOC_LARGE" naming
// was replaced with capitalized multi-word names. Most key material lives
// in the actual allocation areas (Tiny/Large) rather than the metadata
// descriptors, so we keep both kinds.
var FilterRegionTypes27 = []string{
	"Malloc Nano metadata",
	"Malloc Tiny",
	"Malloc Large",
	"Malloc Metadata",
	"Malloc Metadata metadata",
}

type MemRegion struct {
	RegionType   string
	Start        uint64
	End          uint64
	VSize        uint64 // Size in bytes
	RSDNT        uint64 // Resident memory size in bytes (new field)
	SHRMOD       string
	Permissions  string
	RegionDetail string
	Empty        bool
}

func GetVmmap(pid uint32) ([]MemRegion, error) {
	// Execute vmmap command
	cmd := exec.Command(CommandVmmap, "-wide", fmt.Sprintf("%d", pid))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, errors.RunCmdFailed(err)
	}

	// Parse the output using the existing LoadVmmap function
	return LoadVmmap(string(output))
}

func LoadVmmap(output string) ([]MemRegion, error) {
	var regions []MemRegion

	scanner := bufio.NewScanner(strings.NewReader(output))

	// Skip lines until we find the header
	foundHeader := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "==== Writable regions for") {
			foundHeader = true
			// Skip the column headers line
			scanner.Scan()
			break
		}
	}

	if !foundHeader {
		return nil, nil // No vmmap data found
	}

	// Regular expression to parse the vmmap output lines
	// Format: REGION TYPE                    START - END         [ VSIZE  RSDNT  DIRTY   SWAP] PRT/MAX SHRMOD PURGE    REGION DETAIL
	// Updated regex to capture RSDNT value (second value in brackets).
	// The RegionType column can be multi-word (e.g. "Malloc Nano metadata"
	// on Darwin 27), so we capture one or more whitespace-separated words
	// lazily until the START-END hex address is matched.
	re := regexp.MustCompile(`^(\S+(?:\s+\S+)*?)\s+([0-9a-f]+)-([0-9a-f]+)\s+\[\s*(\S+)\s+(\S+)(?:\s+\S+){2}\]\s+(\S+)\s+(\S+)(?:\s+\S+)?\s+(.*)$`)

	// Parse each line
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		matches := re.FindStringSubmatch(line)
		if len(matches) >= 9 { // Updated to check for at least 9 matches

			// Parse start and end addresses
			start, _ := strconv.ParseUint(matches[2], 16, 64)
			end, _ := strconv.ParseUint(matches[3], 16, 64)

			// Parse VSize as numeric value
			vsize := parseSize(matches[4])

			// Parse RSDNT as numeric value (new)
			rsdnt := parseSize(matches[5])

			region := MemRegion{
				RegionType:   strings.TrimSpace(matches[1]),
				Start:        start,
				End:          end,
				VSize:        vsize,
				RSDNT:        rsdnt,                         // Add the new RSDNT field
				Permissions:  matches[6],                    // Shifted index
				SHRMOD:       matches[7],                    // Shifted index
				RegionDetail: strings.TrimSpace(matches[8]), // Shifted index
				Empty:        strings.Contains(line, EmptyFlag),
			}

			regions = append(regions, region)
		}
	}

	return regions, nil
}

// DarwinVersion returns the Darwin kernel version string (e.g., "25.0.0")
func DarwinVersion() string {
	cmd := exec.Command(CommandUname, "-r")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func MemRegionsFilter(regions []MemRegion) []MemRegion {
	var filteredRegions []MemRegion

	// Determine which region type to filter based on Darwin version
	version := DarwinVersion()
	var targetRegionTypes []string
	switch {
	case strings.HasPrefix(version, "27"):
		targetRegionTypes = FilterRegionTypes27
	case strings.HasPrefix(version, "25"):
		targetRegionTypes = []string{FilterRegionType2}
	default:
		targetRegionTypes = []string{FilterRegionType}
	}

	isMatch := func(t string) bool {
		for _, want := range targetRegionTypes {
			if t == want {
				return true
			}
		}
		return false
	}

	for _, region := range regions {
		if region.Empty {
			continue
		}
		if isMatch(region.RegionType) {
			filteredRegions = append(filteredRegions, region)
		}
	}
	return filteredRegions
}

// parseSize converts size strings like "5616K" or "128.0M" to bytes (uint64)
func parseSize(sizeStr string) uint64 {
	// Remove any whitespace
	sizeStr = strings.TrimSpace(sizeStr)

	// Define multipliers for different units
	multipliers := map[string]uint64{
		"B":  1,
		"K":  1024,
		"KB": 1024,
		"M":  1024 * 1024,
		"MB": 1024 * 1024,
		"G":  1024 * 1024 * 1024,
		"GB": 1024 * 1024 * 1024,
	}

	// Regular expression to match numbers with optional decimal point and unit
	// This will match formats like: "5616K", "128.0M", "1.5G", etc.
	re := regexp.MustCompile(`^(\d+(?:\.\d+)?)([KMGB]+)?$`)
	matches := re.FindStringSubmatch(sizeStr)

	if len(matches) < 2 {
		return 0 // No match found
	}

	// Parse the numeric part (which may include a decimal point)
	numStr := matches[1]
	numVal, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0
	}

	// Determine the multiplier based on the unit
	multiplier := uint64(1) // Default if no unit specified
	if len(matches) >= 3 && matches[2] != "" {
		unit := matches[2]
		if m, ok := multipliers[unit]; ok {
			multiplier = m
		}
	}

	// Calculate final size in bytes (rounding to nearest integer)
	return uint64(numVal*float64(multiplier) + 0.5)
}
