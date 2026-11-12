package psc

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Gaming laptops have two GPUs: the processor's integrated one, which drives the
// screen and which programs use by default, and a dedicated AMD or NVIDIA one. The
// "dedicated_gpu" setting runs the game on the dedicated one, the same way GNOME's
// and KDE's "Launch using Dedicated Graphics Card" does (switcheroo-control):
//
//   - NVIDIA's own driver: PRIME render offload (__NV_PRIME_RENDER_OFFLOAD=1,
//     __GLX_VENDOR_LIBRARY_NAME=nvidia, __VK_LAYER_NV_optimus=NVIDIA_only).
//   - Mesa drivers (amdgpu, radeon, nouveau, Intel Arc): DRI_PRIME=pci-<address>,
//     which Mesa's OpenGL loader and Vulkan device-select layer both follow.
//
// Variables that pin Vulkan or EGL to one driver (VK_DRIVER_FILES and friends) are
// removed for the game, or they would keep it on the integrated GPU whatever else
// is set. Heroic's and Lutris's switches leave them, which is why those switches
// can silently do nothing.

// KeyDedicatedGPU is the setting: "1" runs the game on the dedicated GPU.
const KeyDedicatedGPU = "dedicated_gpu"

// GPU is a graphics card found by the kernel's DRM drivers.
type GPU struct {
	Card    string // "card1"
	Slot    string // PCI address, "0000:01:00.0"
	Vendor  uint16 // 0x10de NVIDIA, 0x1002 AMD, 0x8086 Intel
	Device  uint16
	Driver  string // nvidia, amdgpu, i915, xe, nouveau, radeon, ...
	BootVGA bool   // the firmware's display GPU: the one programs use by default
	Name    string // e.g. "NVIDIA GeForce GTX 1050 Mobile"
}

const (
	vendorNVIDIA = 0x10de
	vendorAMD    = 0x1002
	vendorIntel  = 0x8086
)

// pciIDsFiles is where the PCI name list may be. /run/host/usr is the host's
// /usr when running inside the Steam Linux Runtime.
var pciIDsFiles = []string{
	"/usr/share/hwdata/pci.ids", "/usr/share/misc/pci.ids", "/usr/share/pci.ids",
	"/run/host/usr/share/hwdata/pci.ids", "/run/host/usr/share/misc/pci.ids",
}

var (
	cardName = regexp.MustCompile(`^card([0-9]+)$`)
	pciSlot  = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`)
)

// FindGPUs lists the PCI graphics cards on this machine.
func FindGPUs() []GPU { return findGPUs("/sys", pciIDsFiles) }

func findGPUs(sys string, idsFiles []string) []GPU {
	cards, _ := filepath.Glob(filepath.Join(sys, "class", "drm", "card*"))
	sort.Slice(cards, func(i, j int) bool { return cardNumber(cards[i]) < cardNumber(cards[j]) })
	var gpus []GPU
	seen := map[string]bool{}
	for _, card := range cards {
		if !cardName.MatchString(filepath.Base(card)) {
			continue // a connector such as card0-DP-1
		}
		dev := filepath.Join(card, "device")
		real, err := filepath.EvalSymlinks(dev)
		if err != nil {
			continue
		}
		slot := filepath.Base(real)
		if !pciSlot.MatchString(slot) || seen[slot] {
			continue // not a PCI device (e.g. simpledrm), or already listed
		}
		vendor, ok1 := readHex(filepath.Join(dev, "vendor"))
		device, ok2 := readHex(filepath.Join(dev, "device"))
		if !ok1 || !ok2 {
			continue
		}
		seen[slot] = true
		g := GPU{Card: filepath.Base(card), Slot: slot, Vendor: vendor, Device: device}
		if b, err := os.ReadFile(filepath.Join(dev, "boot_vga")); err == nil {
			g.BootVGA = strings.TrimSpace(string(b)) == "1"
		}
		if d, err := os.Readlink(filepath.Join(dev, "driver")); err == nil {
			g.Driver = filepath.Base(d)
		}
		gpus = append(gpus, g)
	}
	names := pciNames(idsFiles, gpus)
	for i := range gpus {
		gpus[i].Name = names[i]
	}
	return gpus
}

func cardNumber(p string) int {
	m := cardName.FindStringSubmatch(filepath.Base(p))
	if m == nil {
		return 1 << 30
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func readHex(p string) (uint16, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(string(b)), "0x"), 16, 16)
	return uint16(v), err == nil
}

// integratedIntel is the processor's own Intel GPU, which always sits at 00:02.0.
func (g GPU) integratedIntel() bool {
	return g.Vendor == vendorIntel && strings.HasSuffix(g.Slot, ":00:02.0")
}

// DedicatedGPU picks the GPU for the dedicated_gpu setting: one that is not the
// default (boot) GPU, preferring NVIDIA, then AMD. When there is none, it says why.
func DedicatedGPU(gpus []GPU) (GPU, bool, string) {
	if len(gpus) < 2 {
		return GPU{}, false, "only one GPU found"
	}
	var cands []GPU
	for _, g := range gpus {
		if !g.BootVGA && !g.integratedIntel() {
			cands = append(cands, g)
		}
	}
	if len(cands) == 0 {
		for _, g := range gpus {
			if g.BootVGA {
				return GPU{}, false, "the game already runs on " + g.Name
			}
		}
		return GPU{}, false, "no dedicated GPU found"
	}
	rank := func(g GPU) int {
		switch g.Vendor {
		case vendorNVIDIA:
			return 0
		case vendorAMD:
			return 1
		}
		return 2
	}
	sort.SliceStable(cands, func(i, j int) bool { return rank(cands[i]) < rank(cands[j]) })
	return cands[0], true, ""
}

// gpuPinVars pin Vulkan, EGL or Mesa's device choice to one GPU.
var gpuPinVars = []string{"VK_DRIVER_FILES", "VK_ICD_FILENAMES", "__EGL_VENDOR_LIBRARY_FILENAMES", "MESA_VK_DEVICE_SELECT"}

// Env is what to set and what to remove so Wine and Proton render on this GPU.
func (g GPU) Env() (set, unset []string) {
	if g.Driver == "nvidia" {
		set = []string{
			"__NV_PRIME_RENDER_OFFLOAD=1",
			"__GLX_VENDOR_LIBRARY_NAME=nvidia",
			"__VK_LAYER_NV_optimus=NVIDIA_only",
		}
	} else {
		set = []string{"DRI_PRIME=pci-" + strings.NewReplacer(":", "_", ".", "_").Replace(g.Slot)}
	}
	return set, gpuPinVars
}

// pciNames names each GPU from the PCI ID list, e.g. "AMD Radeon RX 9070 XT".
func pciNames(idsFiles []string, gpus []GPU) []string {
	names := make([]string, len(gpus))
	for i, g := range gpus {
		names[i] = fmt.Sprintf("%s GPU (%s)", vendorShort(g.Vendor, ""), strings.TrimPrefix(g.Slot, "0000:"))
	}
	if len(gpus) == 0 {
		return names
	}
	var f *os.File
	for _, p := range idsFiles {
		if fh, err := os.Open(p); err == nil {
			f = fh
			break
		}
	}
	if f == nil {
		return names
	}
	defer f.Close()
	vendorNames := map[uint16]string{}
	var cur uint16
	inWanted := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] != '\t' { // vendor line: "10de  NVIDIA Corporation"
			if strings.HasPrefix(line, "C ") {
				break // device classes follow the vendors
			}
			id, name, ok := splitID(line)
			cur, inWanted = id, false
			if ok {
				for _, g := range gpus {
					if g.Vendor == id {
						inWanted = true
						vendorNames[id] = name
					}
				}
			}
			continue
		}
		if !inWanted || strings.HasPrefix(line, "\t\t") {
			continue
		}
		id, name, ok := splitID(line[1:])
		if !ok {
			continue
		}
		if i := strings.Index(name, "["); i >= 0 && strings.HasSuffix(name, "]") {
			name = name[i+1 : len(name)-1] // "GP107M [GeForce GTX 1050 Mobile]"
		}
		for i, g := range gpus {
			if g.Vendor == cur && g.Device == id {
				names[i] = vendorShort(cur, vendorNames[cur]) + " " + name
			}
		}
	}
	return names
}

func splitID(line string) (uint16, string, bool) {
	h, name, ok := strings.Cut(line, "  ")
	if !ok {
		return 0, "", false
	}
	v, err := strconv.ParseUint(h, 16, 16)
	return uint16(v), strings.TrimSpace(name), err == nil
}

func vendorShort(id uint16, full string) string {
	switch id {
	case vendorNVIDIA:
		return "NVIDIA"
	case vendorAMD:
		return "AMD"
	case vendorIntel:
		return "Intel"
	}
	if full != "" {
		return full
	}
	return fmt.Sprintf("PCI %04x", id)
}
