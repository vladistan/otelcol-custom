package psireceiver

import (
	"regexp"
	"strings"
)

type cgroupKind int

const (
	kindOther cgroupKind = iota
	kindPod
	kindContainer
)

// cgroupPath is what a path relative to the cgroup root says about the workload.
type cgroupPath struct {
	Kind        cgroupKind
	PodUID      string
	QoS         string
	ContainerID string
}

var (
	// kubepods-burstable-pod<uid>.slice, kubepods-besteffort-pod<uid>.slice and kubepods-pod<uid>.slice.
	systemdPodName = regexp.MustCompile(`^kubepods(?:-(burstable|besteffort))?-pod([0-9a-fA-F_]{36})\.slice$`)
	// pod<uid> below kubepods, kubepods/burstable or kubepods/besteffort.
	cgroupfsPodName = regexp.MustCompile(`^pod([0-9a-fA-F-]{36})$`)
	containerScope  = regexp.MustCompile(`^(?:cri-containerd|crio|docker)-([0-9a-f]{64})\.scope$`)
	bareContainer   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	uidPattern      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// parseCgroupPath reads rel, a slash-separated path below the cgroup root, for a pod uid, a QoS class and a
// container id. A path that matches no known layout comes back as kindOther with nothing set.
func parseCgroupPath(rel string) cgroupPath {
	segments := strings.Split(rel, "/")
	leaf := segments[len(segments)-1]

	if uid, qos, ok := podFromSegments(segments); ok {
		return cgroupPath{Kind: kindPod, PodUID: uid, QoS: qos}
	}
	id := containerID(leaf)
	if id == "" {
		return cgroupPath{}
	}
	if len(segments) > 1 {
		if uid, qos, ok := podFromSegments(segments[:len(segments)-1]); ok {
			return cgroupPath{Kind: kindContainer, PodUID: uid, QoS: qos, ContainerID: id}
		}
	}
	return cgroupPath{Kind: kindContainer, ContainerID: id}
}

// podFromSegments reports the pod uid and QoS class when the last segment is a pod cgroup under kubepods.
func podFromSegments(segments []string) (uid, qos string, ok bool) {
	leaf := segments[len(segments)-1]
	if match := systemdPodName.FindStringSubmatch(leaf); match != nil && strings.HasPrefix(segments[0], "kubepods") {
		uid, ok = normalizeUID(strings.ReplaceAll(match[2], "_", "-"))
		return uid, qosOrGuaranteed(match[1]), ok
	}
	if match := cgroupfsPodName.FindStringSubmatch(leaf); match != nil && segments[0] == "kubepods" {
		uid, ok = normalizeUID(match[1])
		if len(segments) == 3 {
			return uid, qosOrGuaranteed(segments[1]), ok && (segments[1] == "burstable" || segments[1] == "besteffort")
		}
		return uid, "guaranteed", ok && len(segments) == 2
	}
	return "", "", false
}

func normalizeUID(candidate string) (string, bool) {
	lower := strings.ToLower(candidate)
	return lower, uidPattern.MatchString(lower)
}

func qosOrGuaranteed(qos string) string {
	if qos == "" {
		return "guaranteed"
	}
	return qos
}

func containerID(leaf string) string {
	if match := containerScope.FindStringSubmatch(leaf); match != nil {
		return match[1]
	}
	if bareContainer.MatchString(leaf) {
		return leaf
	}
	return ""
}
