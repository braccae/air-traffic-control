package nodeapi

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ProbedHardware represents the structured hardware discovery findings
// extracted from a remote Linux host.
type ProbedHardware struct {
	CPU           string `json:"cpu"`
	VCPUs         int    `json:"vcpus"`
	RamGiB        int    `json:"ramGiB"`
	Motherboard   string `json:"motherboard"`
	BIOS          string `json:"bios"`
	OSDescription string `json:"osDescription"`
	Arch          string `json:"arch"`
	SecureBoot    string `json:"secureBoot"`
	IOMMU         string `json:"iommu"`
}

// ConnectionRoutes specifies primary and fallback host addresses for network routing.
type ConnectionRoutes struct {
	Primary  string  `json:"primary"`
	Fallback *string `json:"fallback,omitempty"`
}

var (
	rePretty   = regexp.MustCompile(`PRETTY_NAME="?([^"\n\r]+)"?`)
	reName     = regexp.MustCompile(`(?m)^NAME="?([^"\n\r]+)"?`)
	reVer      = regexp.MustCompile(`(?m)^VERSION="?([^"\n\r]+)"?`)
	reProc     = regexp.MustCompile(`(?im)^processor\s*:\s*\d+`)
	reModel    = regexp.MustCompile(`(?im)^model name\s*:\s*([^\n\r]+)`)
	reArmModel = regexp.MustCompile(`(?im)^Model\s*:\s*([^\n\r]+)`)
	reHw       = regexp.MustCompile(`(?im)^Hardware\s*:\s*([^\n\r]+)`)
	reSpaces   = regexp.MustCompile(`\s+`)
	reMemTotal = regexp.MustCompile(`(?i)MemTotal:\s*(\d+)\s*kB`)
	reFree     = regexp.MustCompile(`(?i)Mem:\s*(\d+)`)
	reCatErr   = regexp.MustCompile(`(?i)cat:.*(Permission denied|No such file).*`)
)

// ProbeCommand returns the exact shell pipeline used to discover system hardware.
func ProbeCommand() string {
	parts := []string{
		`echo "===ARCH==="`,
		`uname -m 2>/dev/null || echo "x86_64"`,
		`echo "===OS_RELEASE==="`,
		`cat /etc/os-release 2>/dev/null || cat /usr/lib/os-release 2>/dev/null || echo "PRETTY_NAME=\"Linux\""`,
		`echo "===CPUINFO==="`,
		`cat /proc/cpuinfo 2>/dev/null || echo "model name: Generic CPU"`,
		`echo "===MEMINFO==="`,
		`cat /proc/meminfo 2>/dev/null || free -m 2>/dev/null || echo "MemTotal: 4194304 kB"`,
		`echo "===DMI==="`,
		`cat /sys/class/dmi/id/board_vendor 2>/dev/null || echo ""`,
		`echo "---"`,
		`cat /sys/class/dmi/id/board_name 2>/dev/null || echo ""`,
		`echo "---"`,
		`cat /sys/class/dmi/id/product_name 2>/dev/null || echo ""`,
		`echo "---"`,
		`cat /sys/class/dmi/id/bios_vendor 2>/dev/null || echo ""`,
		`echo "---"`,
		`cat /sys/class/dmi/id/bios_version 2>/dev/null || echo ""`,
		`echo "---"`,
		`cat /sys/class/dmi/id/bios_date 2>/dev/null || echo ""`,
		`echo "===EFI==="`,
		`test -d /sys/firmware/efi && echo "efi" || echo "legacy"`,
		`echo "===IOMMU==="`,
		`ls /sys/kernel/iommu_groups 2>/dev/null | head -n 1 || echo ""`,
	}
	return strings.Join(parts, "; ")
}

// ParseOsRelease parses /etc/os-release contents into a descriptive OS string.
func ParseOsRelease(content string) string {
	if strings.TrimSpace(content) == "" {
		return "Linux (Generic)"
	}

	if match := rePretty.FindStringSubmatch(content); len(match) > 1 {
		val := strings.TrimSpace(match[1])
		if val != "" {
			return val
		}
	}

	nameMatch := reName.FindStringSubmatch(content)
	if len(nameMatch) > 1 && strings.TrimSpace(nameMatch[1]) != "" {
		name := strings.TrimSpace(nameMatch[1])
		ver := ""
		verMatch := reVer.FindStringSubmatch(content)
		if len(verMatch) > 1 {
			ver = strings.TrimSpace(verMatch[1])
		}
		if ver != "" {
			return name + " " + ver
		}
		return name
	}

	return "Linux (Generic)"
}

// ParseCpuInfo parses /proc/cpuinfo contents into CPU model name and vCPU count.
func ParseCpuInfo(content string) (string, int) {
	if strings.TrimSpace(content) == "" {
		return "x86_64 / Generic Processor", 1
	}

	procMatches := reProc.FindAllString(content, -1)
	vcpus := len(procMatches)
	if vcpus < 1 {
		vcpus = 1
	}

	if match := reModel.FindStringSubmatch(content); len(match) > 1 {
		val := strings.TrimSpace(match[1])
		if val != "" {
			return reSpaces.ReplaceAllString(val, " "), vcpus
		}
	}

	if match := reArmModel.FindStringSubmatch(content); len(match) > 1 {
		val := strings.TrimSpace(match[1])
		if val != "" {
			return reSpaces.ReplaceAllString(val, " "), vcpus
		}
	}

	if match := reHw.FindStringSubmatch(content); len(match) > 1 {
		val := strings.TrimSpace(match[1])
		if val != "" {
			return fmt.Sprintf("%s (ARM64)", reSpaces.ReplaceAllString(val, " ")), vcpus
		}
	}

	return "x86_64 / Generic Processor", vcpus
}

// ParseMemInfo parses memory total in GiB from /proc/meminfo or free -m output.
func ParseMemInfo(content string) int {
	if strings.TrimSpace(content) == "" {
		return 1
	}

	if match := reMemTotal.FindStringSubmatch(content); len(match) > 1 {
		kb, err := strconv.ParseFloat(match[1], 64)
		if err == nil {
			gib := int(math.Round(kb / (1024.0 * 1024.0)))
			if gib < 1 {
				return 1
			}
			return gib
		}
	}

	if match := reFree.FindStringSubmatch(content); len(match) > 1 {
		mb, err := strconv.ParseFloat(match[1], 64)
		if err == nil {
			gib := int(math.Round(mb / 1024.0))
			if gib < 1 {
				return 1
			}
			return gib
		}
	}

	return 1
}

func cleanDmi(val string) string {
	trimmed := strings.TrimSpace(val)
	if trimmed == "" {
		return ""
	}
	trimmed = reCatErr.ReplaceAllString(trimmed, "")
	trimmed = strings.TrimSpace(trimmed)
	lower := strings.ToLower(trimmed)
	if lower == "" || lower == "none" || lower == "system product name" {
		return ""
	}
	return trimmed
}

// ParseDmiInfo parses sysfs DMI fields into motherboard and BIOS description strings.
func ParseDmiInfo(content string) (string, string) {
	rawParts := strings.Split(content, "---")
	parts := make([]string, 6)
	for i := 0; i < len(rawParts) && i < 6; i++ {
		parts[i] = cleanDmi(rawParts[i])
	}

	boardVendor := parts[0]
	boardName := parts[1]
	productName := parts[2]
	biosVendor := parts[3]
	biosVersion := parts[4]
	biosDate := parts[5]

	motherboard := ""
	if boardVendor != "" || boardName != "" || productName != "" {
		brand := boardVendor
		model := boardName
		if model == "" {
			model = productName
		}
		extra := ""
		if productName != "" && productName != boardName && productName != brand {
			extra = fmt.Sprintf(" (%s)", productName)
		}
		motherboard = reSpaces.ReplaceAllString(strings.TrimSpace(fmt.Sprintf("%s %s%s", brand, model, extra)), " ")
	}

	if len(motherboard) < 3 {
		motherboard = "Homelab Host Platform"
	}

	bios := ""
	if biosVendor != "" || biosVersion != "" || biosDate != "" {
		bVendor := ""
		if biosVendor != "" {
			bVendor = biosVendor + " "
		}
		bVer := biosVersion
		if bVer == "" {
			bVer = "UEFI"
		}
		bDate := ""
		if biosDate != "" {
			bDate = fmt.Sprintf(" (%s)", biosDate)
		}
		bios = reSpaces.ReplaceAllString(strings.TrimSpace(fmt.Sprintf("%s%s%s", bVendor, bVer, bDate)), " ")
	}

	if bios == "" {
		bios = "UEFI / SeaBIOS"
	}

	return motherboard, bios
}

// ParseHardwareProbeOutput parses raw stdout from ProbeCommand into ProbedHardware.
func ParseHardwareProbeOutput(raw string) ProbedHardware {
	if strings.TrimSpace(raw) == "" {
		return ProbedHardware{
			CPU:           "x86_64 Host Processor",
			VCPUs:         4,
			RamGiB:        8,
			Motherboard:   "Standard Homelab Platform",
			BIOS:          "UEFI 2.8",
			OSDescription: "Linux (Generic)",
			Arch:          "x86_64",
			SecureBoot:    "Disabled",
			IOMMU:         "Disabled",
		}
	}

	sections := make(map[string]string)
	sectionKeys := []string{"ARCH", "OS_RELEASE", "CPUINFO", "MEMINFO", "DMI", "EFI", "IOMMU"}

	for i, key := range sectionKeys {
		marker := fmt.Sprintf("===%s===", key)
		startIdx := strings.Index(raw, marker)
		if startIdx == -1 {
			continue
		}

		contentStart := startIdx + len(marker)
		nextStart := len(raw)

		for j, otherKey := range sectionKeys {
			if i == j {
				continue
			}
			otherMarker := fmt.Sprintf("===%s===", otherKey)
			oIdx := strings.Index(raw[contentStart:], otherMarker)
			if oIdx != -1 {
				absIdx := contentStart + oIdx
				if absIdx < nextStart {
					nextStart = absIdx
				}
			}
		}

		sections[key] = strings.TrimSpace(raw[contentStart:nextStart])
	}

	arch := "x86_64"
	if s, ok := sections["ARCH"]; ok && len(s) > 0 {
		fields := strings.Fields(s)
		if len(fields) > 0 {
			arch = fields[0]
		}
	}

	osDescription := ParseOsRelease(sections["OS_RELEASE"])
	cpuModel, vcpus := ParseCpuInfo(sections["CPUINFO"])
	ramGiB := ParseMemInfo(sections["MEMINFO"])
	motherboard, bios := ParseDmiInfo(sections["DMI"])

	secureBoot := "Disabled"
	if strings.Contains(sections["EFI"], "efi") {
		secureBoot = "Enabled"
	}

	iommu := "Disabled"
	if strings.TrimSpace(sections["IOMMU"]) != "" {
		if strings.Contains(strings.ToLower(cpuModel), "amd") {
			iommu = "Enabled (AMD-Vi)"
		} else {
			iommu = "Enabled (VT-d)"
		}
	}

	return ProbedHardware{
		CPU:           cpuModel,
		VCPUs:         vcpus,
		RamGiB:        ramGiB,
		Motherboard:   motherboard,
		BIOS:          bios,
		OSDescription: osDescription,
		Arch:          arch,
		SecureBoot:    secureBoot,
		IOMMU:         iommu,
	}
}

// ResolveConnectionRoute resolves the primary and fallback network routes for a node.
func ResolveConnectionRoute(node *Object) ConnectionRoutes {
	tailscaleHost, _ := node.GetString("tailscaleHost")
	tailscaleIp, _ := node.GetString("tailscaleIp")
	lanIp, _ := node.GetString("ip")
	fallbackHost, _ := node.GetString("fallbackHost")
	fallbackIp, _ := node.GetString("fallbackIp")

	tailscaleHost = strings.TrimSpace(tailscaleHost)
	tailscaleIp = strings.TrimSpace(tailscaleIp)
	lanIp = strings.TrimSpace(lanIp)
	fallbackHost = strings.TrimSpace(fallbackHost)
	fallbackIp = strings.TrimSpace(fallbackIp)

	primary := tailscaleHost
	if primary == "" {
		primary = tailscaleIp
	}
	if primary == "" {
		primary = lanIp
	}
	if primary == "" {
		primary = "localhost"
	}

	var fallback *string
	if fallbackHost != "" && fallbackHost != primary {
		fallback = &fallbackHost
	} else if fallbackIp != "" && fallbackIp != primary {
		fallback = &fallbackIp
	} else if lanIp != "" && lanIp != primary {
		fallback = &lanIp
	}

	return ConnectionRoutes{
		Primary:  primary,
		Fallback: fallback,
	}
}

// ApplyProbe applies probed hardware telemetry to an existing node object,
// matching TypeScript applyHardwareProbeToNode semantics.
func ApplyProbe(node *Object, probed ProbedHardware) *Object {
	updated := node.Clone()
	if probed.OSDescription != "" {
		updated.SetString("osDescription", probed.OSDescription)
	}

	specsObj := NewObject()
	if raw, ok := node.Get("specs"); ok && len(raw) > 0 && string(raw) != "null" {
		if parsed, err := ParseObject(raw); err == nil {
			specsObj = parsed
		}
	}

	if probed.CPU != "" {
		specsObj.SetString("cpu", probed.CPU)
	} else if !specsObj.Has("cpu") {
		specsObj.SetString("cpu", "")
	}

	if probed.VCPUs > 0 {
		specsObj.SetJSON("vcpus", probed.VCPUs)
	} else if !specsObj.Has("vcpus") {
		specsObj.SetJSON("vcpus", 1)
	}

	if probed.RamGiB > 0 {
		specsObj.SetJSON("ramGiB", probed.RamGiB)
	} else if !specsObj.Has("ramGiB") {
		specsObj.SetJSON("ramGiB", 1)
	}

	if probed.Motherboard != "" {
		specsObj.SetString("motherboard", probed.Motherboard)
	} else if !specsObj.Has("motherboard") {
		specsObj.SetString("motherboard", "")
	}

	if probed.BIOS != "" {
		specsObj.SetString("bios", probed.BIOS)
	} else if !specsObj.Has("bios") {
		specsObj.SetString("bios", "")
	}

	if probed.SecureBoot != "" {
		specsObj.SetString("secureBoot", probed.SecureBoot)
	} else if !specsObj.Has("secureBoot") {
		specsObj.SetString("secureBoot", "Disabled")
	}

	if probed.IOMMU != "" {
		specsObj.SetString("iommu", probed.IOMMU)
	} else if !specsObj.Has("iommu") {
		specsObj.SetString("iommu", "Disabled")
	}

	if ipmi, ok := specsObj.GetString("ipmi"); !ok || ipmi == "" {
		specsObj.SetString("ipmi", "DHCP")
	}
	if ecc, ok := specsObj.GetString("ecc"); !ok || ecc == "" {
		specsObj.SetString("ecc", "Non-ECC")
	}

	specsBytes, _ := specsObj.MarshalJSON()
	updated.Set("specs", specsBytes)
	return updated
}
