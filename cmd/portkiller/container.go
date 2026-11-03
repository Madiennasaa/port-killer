package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// psFormat is the `ps --format` template docker and podman both understand.
const psFormat = "{{.ID}}|{{.Names}}|{{.Ports}}|{{.Status}}"

// proxyProcs are host processes that only hold a socket on behalf of a
// container (rootless podman's port forwarder, CNI-era conmon). When a
// container already reports the same port, the proxy row is noise and
// stopping it would break the container instead of stopping it.
var proxyProcs = map[string]bool{"rootlessport": true, "rootlessport-ch": true, "conmon": true}

// containerName (pure) is the process name without a "docker:" or "podman:"
// runtime prefix; host process names are returned unchanged.
func containerName(it PortItem) string {
	for _, rt := range []string{"docker:", "podman:"} {
		if strings.HasPrefix(it.Process, rt) {
			return strings.TrimPrefix(it.Process, rt)
		}
	}
	return it.Process
}

// runtimeOf returns the container runtime binary name for an item or a
// captured restart, defaulting to docker.
func runtimeOf(source string) string {
	if source == "podman" {
		return "podman"
	}
	return "docker"
}

// podmanUsers lists the accounts whose podman containers are scanned: the
// current user, plus the invoking user when running as root via sudo, since
// rootless containers are only visible to their owner.
func podmanUsers() []string {
	users := []string{""}
	if su := desktopUser(); su != "" {
		users = append(users, su)
	}
	return users
}

// containerArgv builds the command line for runtime under user ("" = the
// current account).
func containerArgv(runtime, user string, args ...string) (string, []string, error) {
	bin := dockerBin
	if runtime == "podman" {
		bin = podmanBin
	}
	return asUserArgv(user, bin, args...)
}

// containerCmd runs a runtime subcommand and folds stderr into the error.
func containerCmd(runtime, user string, args ...string) error {
	bin, argv, err := containerArgv(runtime, user, args...)
	if err != nil {
		return err
	}
	out, err := commandCombined(bin, argv...)
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func GetDockerPorts() []PortItem {
	out, err := commandOutput(dockerBin, "ps", "--format", psFormat)
	if err != nil {
		return nil
	}
	return parseContainerPS(string(out), "docker")
}

// GetPodmanPorts scans podman for every account in podmanUsers. A missing
// podman binary is skipped without spawning anything on each refresh.
func GetPodmanPorts() []PortItem {
	if !filepath.IsAbs(podmanBin) {
		return nil
	}
	var items []PortItem
	for _, user := range podmanUsers() {
		bin, argv, err := containerArgv("podman", user, "ps", "--format", psFormat)
		if err != nil {
			continue
		}
		out, err := commandOutput(bin, argv...)
		if err != nil {
			continue
		}
		for _, it := range parseContainerPS(string(out), "podman") {
			it.ctrUser = user
			items = append(items, it)
		}
	}
	return items
}

// containerPorts merges docker and podman results. A container reported by
// both (podman-docker's docker shim) is kept once, as docker.
func containerPorts() []PortItem {
	items := GetDockerPorts()
	seen := map[string]bool{}
	for _, it := range items {
		seen[it.Proto+"-"+it.Port+"-"+it.PID] = true
	}
	for _, it := range GetPodmanPorts() {
		if !seen[it.Proto+"-"+it.Port+"-"+it.PID] {
			items = append(items, it)
		}
	}
	return items
}

// dropContainerProxies (pure) removes proxyProcs rows whose port is already
// reported by a container. A proxy with no matching container is kept so the
// listener does not silently disappear from the table.
func dropContainerProxies(items []PortItem) []PortItem {
	published := map[string]bool{}
	for _, it := range items {
		if it.IsContainer() {
			published[it.Proto+"-"+it.Port] = true
		}
	}
	out := items[:0]
	for _, it := range items {
		if !it.IsContainer() && proxyProcs[it.Process] && published[strings.TrimSuffix(it.Proto, "6")+"-"+it.Port] {
			continue
		}
		out = append(out, it)
	}
	return out
}

// StopContainer stops a container through its own runtime and account.
func StopContainer(it PortItem) error {
	return containerCmd(runtimeOf(it.Source), it.ctrUser, "stop", it.PID)
}
