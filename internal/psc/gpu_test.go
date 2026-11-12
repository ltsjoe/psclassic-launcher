package psc

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testPCIIDs = `# test list
10de  NVIDIA Corporation
	1c8d  GP107M [GeForce GTX 1050 Mobile]
		1043 1b5e  a subsystem line
1002  Advanced Micro Devices, Inc. [AMD/ATI]
	1638  Cezanne [Radeon Vega Series / Radeon Vega Mobile Series]
	7480  Navi 33 [Radeon RX 7600/7600 XT/7600M XT/7600S/7700S / PRO W7600]
8086  Intel Corporation
	3e9b  CoffeeLake-H GT2 [UHD Graphics 630]
C 03  Display controller
`

type fakeGPU struct {
	card, slot, vendor, device, driver, bootVGA string
}

// fakeSys builds the parts of /sys that FindGPUs reads, the way the kernel lays
// them out: class/drm/cardN/device is a link to the PCI device.
func fakeSys(t *testing.T, gpus ...fakeGPU) (string, []string) {
	t.Helper()
	sys := t.TempDir()
	for _, g := range gpus {
		dev := filepath.Join(sys, "devices", "pci0000:00", g.slot)
		drv := filepath.Join(sys, "bus", "pci", "drivers", g.driver)
		card := filepath.Join(sys, "class", "drm", g.card)
		for _, d := range []string{dev, drv, card, card + "-eDP-1"} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		files := map[string]string{"vendor": g.vendor + "\n", "device": g.device + "\n"}
		if g.bootVGA != "" {
			files["boot_vga"] = g.bootVGA + "\n"
		}
		for name, v := range files {
			if err := os.WriteFile(filepath.Join(dev, name), []byte(v), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(drv, filepath.Join(dev, "driver")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(dev, filepath.Join(card, "device")); err != nil {
			t.Fatal(err)
		}
	}
	ids := filepath.Join(t.TempDir(), "pci.ids")
	if err := os.WriteFile(ids, []byte(testPCIIDs), 0o644); err != nil {
		t.Fatal(err)
	}
	return sys, []string{filepath.Join(sys, "missing.ids"), ids}
}

func TestDedicatedGPU(t *testing.T) {
	intel := fakeGPU{"card0", "0000:00:02.0", "0x8086", "0x3e9b", "i915", "1"}
	nvidia := fakeGPU{"card1", "0000:01:00.0", "0x10de", "0x1c8d", "nvidia", "0"}
	apu := fakeGPU{"card1", "0000:05:00.0", "0x1002", "0x1638", "amdgpu", "1"}
	amd := fakeGPU{"card0", "0000:03:00.0", "0x1002", "0x7480", "amdgpu", "0"}
	nouveau := nvidia
	nouveau.driver = "nouveau"
	desktopNV := nvidia
	desktopNV.bootVGA = "1"
	desktopIntel := intel
	desktopIntel.bootVGA = "0"

	tests := []struct {
		name      string
		gpus      []fakeGPU
		ok        bool
		gpuName   string
		set, why  string
		wantSlot  string
		wantCount int
	}{
		{"Intel + NVIDIA laptop", []fakeGPU{intel, nvidia}, true, "NVIDIA GeForce GTX 1050 Mobile",
			"__NV_PRIME_RENDER_OFFLOAD=1 __GLX_VENDOR_LIBRARY_NAME=nvidia __VK_LAYER_NV_optimus=NVIDIA_only", "", "0000:01:00.0", 2},
		{"Intel + NVIDIA on nouveau", []fakeGPU{intel, nouveau}, true, "NVIDIA GeForce GTX 1050 Mobile",
			"DRI_PRIME=pci-0000_01_00_0", "", "0000:01:00.0", 2},
		{"AMD APU + AMD dGPU laptop", []fakeGPU{apu, amd}, true,
			"AMD Radeon RX 7600/7600 XT/7600M XT/7600S/7700S / PRO W7600", "DRI_PRIME=pci-0000_03_00_0", "", "0000:03:00.0", 2},
		{"one GPU", []fakeGPU{amd}, false, "", "", "only one GPU found", "", 1},
		{"desktop, screen on NVIDIA, iGPU on", []fakeGPU{desktopIntel, desktopNV}, false, "", "",
			"the game already runs on NVIDIA GeForce GTX 1050 Mobile", "", 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sys, ids := fakeSys(t, tc.gpus...)
			gpus := findGPUs(sys, ids)
			if len(gpus) != tc.wantCount {
				t.Fatalf("found %d GPUs, want %d: %+v", len(gpus), tc.wantCount, gpus)
			}
			g, ok, why := DedicatedGPU(gpus)
			if ok != tc.ok || why != tc.why {
				t.Fatalf("DedicatedGPU = %v, %q; want %v, %q", ok, why, tc.ok, tc.why)
			}
			if !ok {
				return
			}
			if g.Slot != tc.wantSlot || g.Name != tc.gpuName {
				t.Errorf("picked %s %q, want %s %q", g.Slot, g.Name, tc.wantSlot, tc.gpuName)
			}
			set, unset := g.Env()
			if strings.Join(set, " ") != tc.set {
				t.Errorf("Env set = %q, want %q", strings.Join(set, " "), tc.set)
			}
			if !slices.Contains(unset, "VK_DRIVER_FILES") || !slices.Contains(unset, "__EGL_VENDOR_LIBRARY_FILENAMES") {
				t.Errorf("Env unset = %v", unset)
			}
		})
	}
}

func TestGPUNameWithoutPCIIDs(t *testing.T) {
	sys, _ := fakeSys(t, fakeGPU{"card0", "0000:03:00.0", "0x1002", "0x7480", "amdgpu", "1"})
	gpus := findGPUs(sys, nil)
	if len(gpus) != 1 || gpus[0].Name != "AMD GPU (03:00.0)" {
		t.Fatalf("got %+v", gpus)
	}
}

// TestCommandUsesGPU checks the game's environment: the offload variables are
// set, and the variables that pin Vulkan/EGL to the integrated GPU are gone.
func TestCommandUsesGPU(t *testing.T) {
	t.Setenv("VK_DRIVER_FILES", "/usr/share/vulkan/icd.d/intel_icd.json")
	t.Setenv("__EGL_VENDOR_LIBRARY_FILENAMES", "/usr/share/glvnd/egl_vendor.d/50_mesa.json")
	dir := t.TempDir()
	exe := filepath.Join(dir, GameExe)
	if err := os.WriteFile(exe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	g := GPU{Slot: "0000:01:00.0", Driver: "nvidia"}
	s := Setup{Runner: Runner{Kind: RunnerWine, Path: "/usr/bin/wine"}, Prefix: filepath.Join(dir, "pfx"), Game: dir, GPU: &g}
	cmd, err := s.Command(context.Background(), exe, nil, []string{"WINEDLLOVERRIDES=dinput8=n,b"}, false)
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(cmd.Env, "\n") + "\n"
	for _, want := range []string{"__NV_PRIME_RENDER_OFFLOAD=1\n", "__VK_LAYER_NV_optimus=NVIDIA_only\n", "WINEDLLOVERRIDES=dinput8=n,b\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %q", want)
		}
	}
	for _, gone := range []string{"VK_DRIVER_FILES=", "__EGL_VENDOR_LIBRARY_FILENAMES="} {
		if strings.Contains(env, "\n"+gone) || strings.HasPrefix(env, gone) {
			t.Errorf("env still has %s", gone)
		}
	}

	s.GPU = nil // setting off: the environment is left alone
	cmd, _ = s.Command(context.Background(), exe, nil, nil, false)
	if !slices.Contains(cmd.Env, "VK_DRIVER_FILES=/usr/share/vulkan/icd.d/intel_icd.json") {
		t.Error("without the setting, VK_DRIVER_FILES should be left as it is")
	}
}
