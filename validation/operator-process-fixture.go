// Offline launcher fixture. Never connects to any platform or reads real secrets.
package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strconv"
)

func main() {
	flag.String("operator-settings", "", "ignored offline fixture management path")
	for _, arg := range os.Args[1:] {
		if arg == "login" {
			home := os.Getenv("CODEX_HOME")
			if home == "" {
				os.Exit(5)
			}
			if err := os.WriteFile(filepath.Join(home, "fixture-login-marker.txt"), []byte("fake login used selected executable and home; no authentication"), 0600); err != nil {
				os.Exit(6)
			}
			return
		}
	}
	path := flag.String("config", "", "fixture configuration")
	check := flag.Bool("check", false, "fixture check")
	nativeCheck := flag.Bool("check-native", false, "fixture native handshake")
	owner := flag.Bool("acceptance-owner-only", false, "fixture owner mode")
	flag.Parse()
	data, err := os.ReadFile(*path)
	var cfg struct {
		BoundaryVerified bool
		OwnerOnly        bool
		Groups           []string
		Members          map[string][]string
	}
	if err != nil || json.Unmarshal(data, &cfg) != nil || len(cfg.Groups) != 1 {
		os.Exit(2)
	}
	if *check {
		return
	}
	if *nativeCheck {
		if os.Getenv("CC_OPERATOR_FIXTURE_PROBE_FAIL") == "1" {
			os.Exit(9)
		}
		return
	}
	if (*owner && (cfg.BoundaryVerified || !cfg.OwnerOnly)) || (!*owner && (!cfg.BoundaryVerified || cfg.OwnerOnly)) || len(cfg.Members["fixture-a"]) != 1 || os.Getenv("CC_GROUP_BOT_ID") != "fixture-bot" || os.Getenv("CC_GROUP_BOT_SECRET") != "local-fixture-only-not-a-credential" {
		os.Exit(3)
	}
	mode := "formal"
	if *owner {
		mode = "owner-only"
	}
	if err := os.WriteFile(os.Getenv("CC_OPERATOR_FIXTURE_REPORT"), []byte(mode+" child received fixture credentials; no network"), 0600); err != nil {
		os.Exit(4)
	}
	if countPath := os.Getenv("CC_OPERATOR_FIXTURE_COUNT"); countPath != "" {
		before, _ := os.ReadFile(countPath)
		count, _ := strconv.Atoi(string(before))
		if err := os.WriteFile(countPath, []byte(strconv.Itoa(count+1)), 0600); err != nil {
			os.Exit(8)
		}
	}
	if os.Getenv("CC_OPERATOR_FIXTURE_FAIL") == "1" {
		os.Exit(7)
	}
}
