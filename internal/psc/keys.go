package psc

import (
	"fmt"
	"strconv"
	"strings"
)

// vkNames names the Windows virtual-key numbers a push-to-talk or flashlight key
// can have. Letters and digits are stored as themselves, so they are not here.
var vkNames = map[int]string{
	0x08: "Backspace", 0x09: "Tab", 0x0D: "Enter", 0x10: "Shift", 0x11: "Ctrl", 0x12: "Alt",
	0x13: "Pause", 0x14: "Caps Lock", 0x1B: "Esc", 0x20: "Space",
	0x21: "Page Up", 0x22: "Page Down", 0x23: "End", 0x24: "Home",
	0x25: "Left", 0x26: "Up", 0x27: "Right", 0x28: "Down",
	0x2C: "Print Screen", 0x2D: "Insert", 0x2E: "Delete",
	0x6A: "Num *", 0x6B: "Num +", 0x6D: "Num -", 0x6E: "Num .", 0x6F: "Num /",
	0x90: "Num Lock", 0x91: "Scroll Lock",
	0xA0: "Left Shift", 0xA1: "Right Shift", 0xA2: "Left Ctrl", 0xA3: "Right Ctrl",
	0xA4: "Left Alt", 0xA5: "Right Alt",
	0xBA: ";", 0xBB: "=", 0xBC: ",", 0xBD: "-", 0xBE: ".", 0xBF: "/", 0xC0: "`",
	0xDB: "[", 0xDC: `\`, 0xDD: "]", 0xDE: "'",
}

func init() {
	for i := 0; i <= 9; i++ {
		vkNames[0x60+i] = "Num " + strconv.Itoa(i)
	}
	for i := 1; i <= 24; i++ {
		vkNames[0x6F+i] = "F" + strconv.Itoa(i)
	}
}

// KeyDisplay is how the Settings window shows a stored key.
func KeyDisplay(key string) string {
	switch key {
	case "Mouse3":
		return "Middle mouse button"
	case "Mouse4":
		return "Mouse button 4 (side)"
	case "Mouse5":
		return "Mouse button 5 (side)"
	case "None", "":
		return "(none)"
	}
	if strings.HasPrefix(key, "VK") {
		if n, err := strconv.Atoi(key[2:]); err == nil {
			if name, ok := vkNames[n]; ok {
				return name
			}
			return fmt.Sprintf("Key %d", n)
		}
	}
	return key
}

// ParseKey reads a key typed in a terminal or found in a settings file: a
// stored form ("Y", "VK120", "Mouse4", "None") or a display name ("F9",
// "Space", "Mouse button 4 (side)", "(none)"), ignoring case.
func ParseKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("no key given")
	}
	for _, k := range []string{"Mouse3", "Mouse4", "Mouse5", "None"} {
		if strings.EqualFold(s, k) || strings.EqualFold(s, KeyDisplay(k)) {
			return k, nil
		}
	}
	if r := []rune(s); len(r) == 1 && ((r[0] >= 'a' && r[0] <= 'z') || (r[0] >= 'A' && r[0] <= 'Z') || (r[0] >= '0' && r[0] <= '9')) {
		return strings.ToUpper(s), nil
	}
	if strings.HasPrefix(strings.ToUpper(s), "VK") {
		if n, err := strconv.Atoi(s[2:]); err == nil && n > 0 && n < 256 {
			return FromKeyNumber(n), nil
		}
	}
	for n, name := range vkNames {
		if strings.EqualFold(s, name) {
			return FromKeyNumber(n), nil
		}
	}
	return "", fmt.Errorf("unknown key %q (try a letter, F1-F24, Space, Shift, Mouse4, Mouse5 or None)", s)
}
