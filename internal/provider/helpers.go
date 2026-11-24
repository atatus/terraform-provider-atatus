package provider

import (
	"fmt"
	"strings"
)

var frequencyToSeconds = map[string]int64{
	"1m":  60,
	"5m":  300,
	"10m": 600,
	"15m": 900,
	"30m": 1800,
	"1h":  3600,
	"6h":  21600,
	"12h": 43200,
	"1d":  86400,
}

var secondsToFrequency = map[int64]string{
	60:    "1m",
	300:   "5m",
	600:   "10m",
	900:   "15m",
	1800:  "30m",
	3600:  "1h",
	21600: "6h",
	43200: "12h",
	86400: "1d",
}

func parseFrequency(freqStr string, defaultSec int64) (int64, string, error) {
	clean := strings.ToLower(strings.TrimSpace(freqStr))
	if clean == "" {
		if s, ok := secondsToFrequency[defaultSec]; ok {
			return defaultSec, s, nil
		}
		return defaultSec, "5m", nil
	}

	if sec, ok := frequencyToSeconds[clean]; ok {
		return sec, clean, nil
	}

	// Also support numeric strings for backward compatibility if matching supported intervals
	switch clean {
	case "60":
		return 60, "1m", nil
	case "300":
		return 300, "5m", nil
	case "600":
		return 600, "10m", nil
	case "900":
		return 900, "15m", nil
	case "1800":
		return 1800, "30m", nil
	case "3600":
		return 3600, "1h", nil
	case "21600":
		return 21600, "6h", nil
	case "43200":
		return 43200, "12h", nil
	case "86400":
		return 86400, "1d", nil
	}

	return 0, "", fmt.Errorf("unsupported frequency '%s'. Supported intervals are: '1m', '5m', '10m', '15m', '30m', '1h', '6h', '12h', '1d'", freqStr)
}

func secondsToFrequencyString(sec int64) string {
	if str, ok := secondsToFrequency[sec]; ok {
		return str
	}
	return "5m"
}

func deduplicateStrings(items []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, item := range items {
		clean := strings.TrimSpace(item)
		if clean != "" && !seen[clean] {
			seen[clean] = true
			result = append(result, clean)
		}
	}
	return result
}

func cleanHost(rawHost string) string {
	h := strings.TrimSpace(rawHost)
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	h = strings.TrimSuffix(h, "/")
	return h
}

func normalizeRunType(rt string) (string, error) {
	clean := strings.ToLower(strings.TrimSpace(rt))
	if clean == "" || clean == "parallel" {
		return "parallel", nil
	}
	if clean == "sequential" {
		return "sequential", nil
	}
	return "", fmt.Errorf("unsupported run_type '%s'. Supported options are: 'parallel', 'sequential'", rt)
}
