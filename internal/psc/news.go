package psc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// StatusURL serves online.json, the player count the launcher shows.
const StatusURL = "https://status.blackarmoredreapers.com"

// ServerInfo is what the launcher shows above and beside the news.
type ServerInfo struct {
	News        string // news.txt, with LF line ends
	GameVersion string // game-version.txt, e.g. "1.3.91"; "" if unknown
	Online      string // "PSC Online: 3   (TR 0 | NC 3 | VS 0)", "Server offline", or "" if unknown
}

var gameVersionRE = regexp.MustCompile(`^[0-9][0-9.]{0,15}$`)

// FetchServerInfo loads the news, the game version and the player count, each
// with one quick try, as the launcher does when it opens. With beta set, the
// beta branch's news and version are preferred when the server has them.
func FetchServerInfo(ctx context.Context, client *http.Client, source, userAgent string, beta bool) ServerInfo {
	var info ServerInfo
	get := func(url string) ([]byte, int, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return b, resp.StatusCode, err
	}
	fromSource := func(name string) []byte {
		stamp := fmt.Sprintf("?t=%d", time.Now().UnixNano()/100)
		if beta {
			b, code, err := get(source + "/beta/" + name + stamp)
			if err == nil {
				return b
			}
			if code != http.StatusNotFound && code != http.StatusForbidden {
				return nil
			}
		}
		b, _, err := get(source + "/" + name + stamp)
		if err != nil {
			return nil
		}
		return b
	}

	if b := fromSource("news.txt"); b != nil {
		info.News = strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n"))
	}
	if b := fromSource("game-version.txt"); b != nil {
		line, _, _ := strings.Cut(string(b), "\n")
		if line = strings.TrimSpace(line); gameVersionRE.MatchString(line) {
			info.GameVersion = line
		}
	}
	status, _, err := get(StatusURL + "/online.json")
	if err != nil {
		status, _, err = get(source + "/status/online.json")
	}
	if err == nil {
		info.Online = onlineText(status, time.Now())
	}
	return info
}

// onlineText formats online.json. The server counts as offline when the count
// is more than six minutes old.
func onlineText(b []byte, now time.Time) string {
	var v map[string]json.Number
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return ""
	}
	n := func(k string) int64 {
		x, err := v[k].Int64()
		if err != nil {
			return -1
		}
		return x
	}
	t := n("time")
	if t < 0 || now.Sub(time.UnixMilli(t)) >= 6*time.Minute {
		return "Server offline"
	}
	return fmt.Sprintf("PSC Online: %d   (TR %d | NC %d | VS %d)", n("total"), n("TR"), n("NC"), n("VS"))
}

// Mb formats a size the way the launcher does: "860.0 MB", "4.37 GB".
func Mb(n int64) string {
	if n < 1<<30 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
}
