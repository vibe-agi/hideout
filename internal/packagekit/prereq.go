package packagekit

import "os/exec"

const (
	PrerequisiteAvailable      = "available"
	PrerequisiteMissing        = "missing"
	PrerequisiteUndiscoverable = "undiscoverable"
)

func ExternalPrerequisites() []ExternalPrerequisiteStatus {
	status := ExternalPrerequisiteStatus{
		Name:         "tun2socks",
		Status:       PrerequisiteAvailable,
		PackageOwned: false,
		Hint:         "external prerequisite for tun2socks privacy profiles",
	}
	if _, err := exec.LookPath("tun2socks"); err != nil {
		status.Status = PrerequisiteMissing
		status.Hint = "install tun2socks or choose a direct/network mode that does not require it"
	}
	return []ExternalPrerequisiteStatus{status}
}
