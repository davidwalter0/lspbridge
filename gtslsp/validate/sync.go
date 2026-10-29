package validate

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

// k8sSchemaBase is the kubeconform-standard source. Its files are named
// <kind>[-<group>]-<version>.json (group = first dotted segment) under
// <vX.Y.Z>-standalone-strict/, exactly what kubeconform's default
// -schema-location template reads and what runKubeconform's template reads.
const k8sSchemaBase = "https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master"

// defaultKinds maps a kind to its apiVersion for the default sync set.
var defaultKinds = map[string]string{
	"Deployment": "apps/v1", "StatefulSet": "apps/v1", "DaemonSet": "apps/v1",
	"Job": "batch/v1", "CronJob": "batch/v1",
	"Service": "v1", "ConfigMap": "v1", "Secret": "v1", "ServiceAccount": "v1",
	"PersistentVolumeClaim": "v1", "Namespace": "v1", "Pod": "v1",
	"Ingress": "networking.k8s.io/v1", "NetworkPolicy": "networking.k8s.io/v1",
	"Role": "rbac.authorization.k8s.io/v1", "RoleBinding": "rbac.authorization.k8s.io/v1",
	"ClusterRole": "rbac.authorization.k8s.io/v1", "ClusterRoleBinding": "rbac.authorization.k8s.io/v1",
	"HorizontalPodAutoscaler": "autoscaling/v2",
}

// DefaultKinds returns the default kind set, sorted.
func DefaultKinds() []string {
	var ks []string
	for k := range defaultKinds {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// K8sSchemaFile is the schema file name for an apiVersion and kind, as
// kubeconform derives it.
func K8sSchemaFile(apiVersion, kind string) string {
	group, version := "", apiVersion
	if i := strings.Index(apiVersion, "/"); i >= 0 {
		group, version = apiVersion[:i], apiVersion[i+1:]
	}
	name := strings.ToLower(kind)
	if group != "" {
		g, _, _ := strings.Cut(group, ".")
		name += "-" + strings.ToLower(g)
	}
	return name + "-" + strings.ToLower(version) + ".json"
}

// K8sSchemaURLs returns the download URLs for kinds at a Kubernetes version.
// A kind is a name from the default set, or "Kind=apiVersion" for any other.
// version defaults to the validator's (GTS_LSP_K8S_VERSION or built-in).
func K8sSchemaURLs(version string, kinds []string) ([]string, error) {
	if version == "" {
		version = k8sVersion()
	}
	version = "v" + strings.TrimPrefix(version, "v")
	if len(kinds) == 0 {
		kinds = DefaultKinds()
	}
	var urls []string
	for _, k := range kinds {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		kind, av, explicit := strings.Cut(k, "=")
		if !explicit {
			found := false
			for name, v := range defaultKinds {
				if strings.EqualFold(name, kind) {
					kind, av, found = name, v, true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("unknown kind %q: use Kind=apiVersion (e.g. Gateway=gateway.networking.k8s.io/v1)", kind)
			}
		}
		urls = append(urls, fmt.Sprintf("%s/%s-standalone-strict/%s", k8sSchemaBase, version, K8sSchemaFile(av, kind)))
	}
	return urls, nil
}

// SyncResult describes one fetched file.
type SyncResult struct {
	URL, Path string
	Bytes     int64
}

// SyncWithReport is SyncSchemas plus a per-file report of what landed in the
// cache. The error lists every failure; results holds the successes.
func SyncWithReport(ctx context.Context, urls []string) ([]SyncResult, error) {
	var res []SyncResult
	var failed []string
	var firstErr error
	for _, u := range urls {
		if err := SyncSchemas(ctx, []string{u}); err != nil {
			failed = append(failed, u)
			if firstErr == nil {
				firstErr = err
			}
			fmt.Fprintln(os.Stderr, "FAIL", err)
			continue
		}
		p := cachePathForURL(u)
		var n int64
		if st, err := os.Stat(p); err == nil {
			n = st.Size()
		}
		res = append(res, SyncResult{URL: u, Path: p, Bytes: n})
	}
	if len(failed) > 0 {
		return res, fmt.Errorf("%d of %d schemas failed to sync", len(failed), len(urls))
	}
	return res, nil
}
