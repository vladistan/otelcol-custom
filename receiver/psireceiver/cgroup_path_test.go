package psireceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	testUID       = "0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9"
	testUIDSystem = "0a1b2c3d_4e5f_6071_8293_a4b5c6d7e8f9"
	testContainer = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" // pragma: allowlist secret
)

func TestParseCgroupPath(t *testing.T) {
	tests := []struct {
		name string
		rel  string
		want cgroupPath
	}{
		{
			name: "systemd burstable pod",
			rel:  "kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod" + testUIDSystem + ".slice",
			want: cgroupPath{Kind: kindPod, PodUID: testUID, QoS: "burstable"},
		},
		{
			name: "systemd besteffort pod",
			rel:  "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod" + testUIDSystem + ".slice",
			want: cgroupPath{Kind: kindPod, PodUID: testUID, QoS: "besteffort"},
		},
		{
			name: "systemd guaranteed pod sits under kubepods.slice",
			rel:  "kubepods.slice/kubepods-pod" + testUIDSystem + ".slice",
			want: cgroupPath{Kind: kindPod, PodUID: testUID, QoS: "guaranteed"},
		},
		{
			name: "cgroupfs burstable pod",
			rel:  "kubepods/burstable/pod" + testUID,
			want: cgroupPath{Kind: kindPod, PodUID: testUID, QoS: "burstable"},
		},
		{
			name: "cgroupfs guaranteed pod",
			rel:  "kubepods/pod" + testUID,
			want: cgroupPath{Kind: kindPod, PodUID: testUID, QoS: "guaranteed"},
		},
		{
			name: "containerd scope under a systemd pod",
			rel:  "kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod" + testUIDSystem + ".slice/cri-containerd-" + testContainer + ".scope",
			want: cgroupPath{Kind: kindContainer, PodUID: testUID, QoS: "burstable", ContainerID: testContainer},
		},
		{
			name: "crio scope",
			rel:  "kubepods.slice/kubepods-pod" + testUIDSystem + ".slice/crio-" + testContainer + ".scope",
			want: cgroupPath{Kind: kindContainer, PodUID: testUID, QoS: "guaranteed", ContainerID: testContainer},
		},
		{
			name: "docker scope",
			rel:  "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod" + testUIDSystem + ".slice/docker-" + testContainer + ".scope",
			want: cgroupPath{Kind: kindContainer, PodUID: testUID, QoS: "besteffort", ContainerID: testContainer},
		},
		{
			name: "bare 64 hex directory under a cgroupfs pod",
			rel:  "kubepods/burstable/pod" + testUID + "/" + testContainer,
			want: cgroupPath{Kind: kindContainer, PodUID: testUID, QoS: "burstable", ContainerID: testContainer},
		},
		{
			name: "system service carries no identity",
			rel:  "system.slice/sshd.service",
			want: cgroupPath{Kind: kindOther},
		},
		{
			name: "qos slice is not a pod",
			rel:  "kubepods.slice/kubepods-burstable.slice",
			want: cgroupPath{Kind: kindOther},
		},
		{
			name: "malformed uid gives no uid",
			rel:  "kubepods.slice/kubepods-burstable.slice/kubepods-burstable-podnot_a_uid.slice",
			want: cgroupPath{Kind: kindOther},
		},
		{
			name: "docker scope on a plain Docker host is a container without a pod",
			rel:  "system.slice/docker-" + testContainer + ".scope",
			want: cgroupPath{Kind: kindContainer, ContainerID: testContainer},
		},
		{
			name: "short container id is not an id",
			rel:  "kubepods.slice/kubepods-pod" + testUIDSystem + ".slice/cri-containerd-abc.scope",
			want: cgroupPath{Kind: kindOther},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseCgroupPath(tt.rel))
		})
	}
}
