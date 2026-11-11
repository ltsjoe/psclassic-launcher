package psc

import (
	"encoding/json"
	"os/exec"
	"strings"
)

// AudioDevices lists microphones and speakers by the names Windows programs
// see under Wine: the PulseAudio/PipeWire device descriptions, cut to the 31
// characters Windows' waveIn/waveOut device names hold. The voice chat program
// looks devices up by these names. Without pactl the lists are empty, and only
// the default device ("Windows default") can be chosen.
func AudioDevices() (mics, speakers []string) {
	type dev struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		MonitorOf    string `json:"monitor_of_sink"`
		MonitorOfOld any    `json:"monitor_of_sink_name"`
	}
	list := func(kind string) []dev {
		out, err := exec.Command("pactl", "-f", "json", "list", kind).Output()
		if err != nil {
			return nil
		}
		var ds []dev
		if json.Unmarshal(out, &ds) != nil {
			return nil
		}
		return ds
	}
	seen := map[string]bool{}
	for _, d := range list("sources") {
		if strings.HasSuffix(d.Name, ".monitor") || (d.MonitorOf != "" && d.MonitorOf != "n/a") {
			continue
		}
		if n := winDeviceName(d.Description); n != "" && !seen["in"+n] {
			seen["in"+n] = true
			mics = append(mics, n)
		}
	}
	for _, d := range list("sinks") {
		if n := winDeviceName(d.Description); n != "" && !seen["out"+n] {
			seen["out"+n] = true
			speakers = append(speakers, n)
		}
	}
	return mics, speakers
}

func winDeviceName(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > 31 {
		r = r[:31]
	}
	return string(r)
}
