package audit

// EvidentiaryKeys are Core-owned audit metadata fields that scripts must not
// delete from product evidence. Broker local audit restores them; export fails
// closed when a redaction selection targets or mutates them.
var EvidentiaryKeys = []string{
	"requestId",
	"subject",
	"command",
	"route",
	"requestedAction",
	"status",
	"error",
}

func IsEvidentiaryKey(key string) bool {
	for _, item := range EvidentiaryKeys {
		if key == item {
			return true
		}
	}
	return false
}
