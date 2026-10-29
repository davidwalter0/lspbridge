package gotmpl

import (
	"os"
	"path/filepath"
	"strings"
)

// IsHelmTemplate reports whether path is a helm chart template: a .yaml,
// .yml or .tpl file beneath a directory named "templates" that has a
// Chart.yaml in some ancestor directory (the chart root, or above it for a
// subchart layout).
func IsHelmTemplate(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml", ".tpl":
	default:
		return false
	}
	dir := filepath.Dir(filepath.Clean(path))
	for {
		parent := filepath.Dir(dir)
		if filepath.Base(dir) == "templates" && hasChartYAML(parent) {
			return true
		}
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// hasChartYAML looks for Chart.yaml in dir and each of its ancestors.
func hasChartYAML(dir string) bool {
	for {
		if st, err := os.Stat(filepath.Join(dir, "Chart.yaml")); err == nil && st.Mode().IsRegular() {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
